#!/usr/bin/env python3
"""Replay Anthropic trace files and simulate prompt-cache behavior.

The simulator models Anthropic Messages prompt caching closely enough for local
strategy tuning:
- prompt order is tools -> system -> messages
- top-level cache_control maps to the last cacheable block
- block-level cache_control marks explicit breakpoints
- cache hits require exact prefix hashes
- each breakpoint checks previously-written cache entries up to a lookback window
- cache entries expire according to 5m/1h TTL and are refreshed on reads
"""

from __future__ import annotations

import argparse
import copy
import dataclasses
import datetime as dt
import hashlib
import json
import math
import re
import sys
from pathlib import Path
from typing import Any


DEFAULT_LOOKBACK_BLOCKS = 20
DEFAULT_MAX_BREAKPOINTS = 4
DEFAULT_TTL_SECONDS = 5 * 60

CACHEABLE_MESSAGE_TYPES = {
    "text",
    "image",
    "document",
    "tool_use",
    "tool_result",
    "server_tool_use",
    "web_search_tool_result",
}


@dataclasses.dataclass
class Block:
    index: int
    path: str
    scope: str
    role: str
    block_type: str
    value: Any
    raw_tokens: int
    prefix_hash: str = ""
    prefix_tokens: int = 0
    cache_control: dict[str, Any] | None = None


@dataclasses.dataclass(frozen=True)
class Breakpoint:
    index: int
    ttl_seconds: int
    source: str


@dataclasses.dataclass
class RequestRecord:
    path: Path
    request_number: int
    captured_at: dt.datetime
    request: dict[str, Any]
    success: bool
    actual_read: int
    actual_create: int
    actual_input: int


@dataclasses.dataclass
class CacheEntry:
    tokens: int
    expires_at: dt.datetime
    ttl_seconds: int


@dataclasses.dataclass
class SimRequestResult:
    request_number: int
    read: int
    create: int
    input_tokens: int
    hit_tokens: int
    breakpoint_tokens: int
    breakpoint_count: int
    hit: bool
    actual_read: int
    actual_create: int
    actual_input: int


@dataclasses.dataclass
class SimSummary:
    strategy: str
    requests: int
    read: int
    create: int
    input_tokens: int
    hits: int
    breakpoints: int
    actual_read: int = 0
    actual_create: int = 0
    actual_input: int = 0
    per_request: list[SimRequestResult] = dataclasses.field(default_factory=list)

    @property
    def read_create_ratio(self) -> float:
        return self.read / self.create if self.create else math.inf

    @property
    def hit_rate(self) -> float:
        return self.hits / self.requests if self.requests else 0.0


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Replay MoonBridge Anthropic traces and simulate prompt caching.",
    )
    parser.add_argument(
        "trace",
        type=Path,
        help="Trace session directory or its Anthropic subdirectory.",
    )
    parser.add_argument(
        "--strategy",
        choices=("observed", "automatic", "tail", "spread", "none"),
        default="observed",
        help="Strategy to simulate when --compare is not set.",
    )
    parser.add_argument(
        "--compare",
        action="store_true",
        help="Run observed, automatic, tail, and spread strategies side by side.",
    )
    parser.add_argument(
        "--lookback-blocks",
        type=int,
        default=DEFAULT_LOOKBACK_BLOCKS,
        help="Approximate Anthropic cache lookback window before each breakpoint.",
    )
    parser.add_argument(
        "--fit-lookback",
        type=int,
        metavar="N",
        help="Fit observed strategy across lookback values 0..N and report the closest match to trace usage.",
    )
    parser.add_argument(
        "--max-breakpoints",
        type=int,
        default=DEFAULT_MAX_BREAKPOINTS,
        help="Maximum block-level breakpoints for generated strategies.",
    )
    parser.add_argument(
        "--ttl",
        default="5m",
        help="Default TTL for generated strategies and cache_control without ttl.",
    )
    parser.add_argument(
        "--min-cache-tokens",
        default="auto",
        help="Minimum cacheable prefix tokens: auto, or an integer override.",
    )
    parser.add_argument(
        "--no-calibrate",
        action="store_true",
        help="Do not scale estimated request tokens to observed usage totals.",
    )
    parser.add_argument(
        "--include-errors",
        action="store_true",
        help="Include trace records without an upstream response. They do not write cache entries.",
    )
    parser.add_argument(
        "--json",
        action="store_true",
        help="Emit JSON instead of a text table.",
    )
    parser.add_argument(
        "--per-request",
        type=Path,
        help="Optional JSONL output path for per-request simulated usage.",
    )
    args = parser.parse_args()

    records = load_records(args.trace, include_errors=args.include_errors)
    if not records:
        print(f"no Anthropic trace records found under {args.trace}", file=sys.stderr)
        return 1

    strategies = ["observed", "automatic", "tail", "spread"] if args.compare else [args.strategy]
    fit_summary: tuple[int, SimSummary] | None = None
    if args.fit_lookback is not None:
        fit_summary = fit_lookback(
            records,
            max_lookback=args.fit_lookback,
            max_breakpoints=args.max_breakpoints,
            default_ttl_seconds=parse_ttl(args.ttl),
            min_cache_tokens_arg=args.min_cache_tokens,
            calibrate=not args.no_calibrate,
        )

    summaries = [
        simulate(
            records,
            strategy=strategy,
            lookback_blocks=args.lookback_blocks,
            max_breakpoints=args.max_breakpoints,
            default_ttl_seconds=parse_ttl(args.ttl),
            min_cache_tokens_arg=args.min_cache_tokens,
            calibrate=not args.no_calibrate,
        )
        for strategy in strategies
    ]

    if args.per_request:
        write_per_request(args.per_request, summaries)

    if args.json:
        payload = {
            "summaries": [summary_to_json(summary) for summary in summaries],
            "fit_lookback": None if fit_summary is None else {
                "lookback_blocks": fit_summary[0],
                "summary": summary_to_json(fit_summary[1]),
            },
        }
        print(json.dumps(payload, ensure_ascii=False, indent=2))
    else:
        print_text_report(records, summaries, fit_summary)
    return 0


def load_records(trace_path: Path, *, include_errors: bool) -> list[RequestRecord]:
    # Support both old layout (session/Anthropic/*.json) and new layout
    # (session/model/Anthropic/*.json) and direct Anthropic directory.
    anthropic_dir = trace_path
    if (trace_path / "Anthropic").is_dir():
        anthropic_dir = trace_path / "Anthropic"
    if not any(anthropic_dir.glob("*.json")):
        # Try model subdirectory layout: session/{model}/Anthropic/*.json
        paths = sorted(trace_path.rglob("Anthropic/*.json"), key=lambda path: numeric_sort_key(path.name))
        if paths:
            return list(load_all(paths, include_errors=include_errors))
    paths = sorted(anthropic_dir.glob("*.json"), key=lambda path: numeric_sort_key(path.name))
    records: list[RequestRecord] = []
    for path in paths:
        with path.open("r", encoding="utf-8") as handle:
            data = json.load(handle)
        request = data.get("anthropic_request")
        if not isinstance(request, dict):
            continue
        success = isinstance(data.get("anthropic_stream_events"), list) or isinstance(data.get("anthropic_response"), dict)
        if not include_errors and not success:
            continue
        records.append(
            RequestRecord(
                path=path,
                request_number=int(data.get("request_number") or numeric_sort_key(path.name)[0]),
                captured_at=parse_time(data.get("captured_at")),
                request=request,
                success=success,
                actual_read=actual_usage(data, "cache_read_input_tokens"),
                actual_create=actual_usage(data, "cache_creation_input_tokens"),
                actual_input=actual_usage(data, "input_tokens"),
            )
        )
    records.sort(key=lambda record: (record.captured_at, record.request_number))
    return records


def simulate(
    records: list[RequestRecord],
    *,
    strategy: str,
    lookback_blocks: int,
    max_breakpoints: int,
    default_ttl_seconds: int,
    min_cache_tokens_arg: str,
    calibrate: bool,
) -> SimSummary:
    cache: dict[str, CacheEntry] = {}
    results: list[SimRequestResult] = []

    for sequence, record in enumerate(records):
        now = record.captured_at
        if now == dt.datetime.min.replace(tzinfo=dt.timezone.utc):
            now = dt.datetime(2026, 1, 1, tzinfo=dt.timezone.utc) + dt.timedelta(seconds=sequence)

        blocks = build_blocks(record.request)
        if not blocks:
            results.append(empty_result(record))
            continue

        assign_prefixes(record.request, blocks, record, calibrate)
        min_cache_tokens = resolve_min_cache_tokens(record.request.get("model", ""), min_cache_tokens_arg)
        breakpoints = choose_breakpoints(
            strategy=strategy,
            request=record.request,
            blocks=blocks,
            max_breakpoints=max_breakpoints,
            default_ttl_seconds=default_ttl_seconds,
        )
        valid_breakpoints = [
            bp for bp in breakpoints if blocks[bp.index].prefix_tokens >= min_cache_tokens
        ]

        hit_index = -1
        hit_tokens = 0
        hit_hash = ""
        for index in sorted(check_indexes(valid_breakpoints, blocks, lookback_blocks)):
            block = blocks[index]
            entry = cache.get(block.prefix_hash)
            if entry is None or entry.expires_at <= now:
                continue
            if block.prefix_tokens >= hit_tokens:
                hit_index = index
                hit_tokens = block.prefix_tokens
                hit_hash = block.prefix_hash

        if hit_hash:
            entry = cache[hit_hash]
            cache[hit_hash] = CacheEntry(
                tokens=entry.tokens,
                expires_at=now + dt.timedelta(seconds=entry.ttl_seconds),
                ttl_seconds=entry.ttl_seconds,
            )

        last_breakpoint_index = max((bp.index for bp in valid_breakpoints), default=-1)
        breakpoint_tokens = blocks[last_breakpoint_index].prefix_tokens if last_breakpoint_index >= 0 else 0
        total_tokens = blocks[-1].prefix_tokens

        read = hit_tokens
        create = max(0, breakpoint_tokens - hit_tokens) if last_breakpoint_index >= 0 else 0
        input_floor = record.actual_input if calibrate and (record.actual_read + record.actual_create + record.actual_input) > 0 else 0
        input_tokens = input_floor + (max(0, total_tokens - breakpoint_tokens) if last_breakpoint_index >= 0 else total_tokens)

        if record.success:
            for bp in valid_breakpoints:
                block = blocks[bp.index]
                cache[block.prefix_hash] = CacheEntry(
                    tokens=block.prefix_tokens,
                    expires_at=now + dt.timedelta(seconds=bp.ttl_seconds),
                    ttl_seconds=bp.ttl_seconds,
                )

        results.append(
            SimRequestResult(
                request_number=record.request_number,
                read=read,
                create=create,
                input_tokens=input_tokens,
                hit_tokens=hit_tokens,
                breakpoint_tokens=breakpoint_tokens,
                breakpoint_count=len(valid_breakpoints),
                hit=hit_index >= 0,
                actual_read=record.actual_read,
                actual_create=record.actual_create,
                actual_input=record.actual_input,
            )
        )

    return SimSummary(
        strategy=strategy,
        requests=len(results),
        read=sum(result.read for result in results),
        create=sum(result.create for result in results),
        input_tokens=sum(result.input_tokens for result in results),
        hits=sum(1 for result in results if result.hit),
        breakpoints=sum(result.breakpoint_count for result in results),
        actual_read=sum(record.actual_read for record in records),
        actual_create=sum(record.actual_create for record in records),
        actual_input=sum(record.actual_input for record in records),
        per_request=results,
    )


def fit_lookback(
    records: list[RequestRecord],
    *,
    max_lookback: int,
    max_breakpoints: int,
    default_ttl_seconds: int,
    min_cache_tokens_arg: str,
    calibrate: bool,
) -> tuple[int, SimSummary]:
    actual_read = sum(record.actual_read for record in records)
    actual_create = sum(record.actual_create for record in records)
    best: tuple[int, SimSummary, int] | None = None
    for lookback in range(max_lookback + 1):
        summary = simulate(
            records,
            strategy="observed",
            lookback_blocks=lookback,
            max_breakpoints=max_breakpoints,
            default_ttl_seconds=default_ttl_seconds,
            min_cache_tokens_arg=min_cache_tokens_arg,
            calibrate=calibrate,
        )
        error = abs(summary.read - actual_read) + abs(summary.create - actual_create)
        if best is None or error < best[2]:
            best = (lookback, summary, error)
    assert best is not None
    return best[0], best[1]


def build_blocks(request: dict[str, Any]) -> list[Block]:
    blocks: list[Block] = []

    for index, tool in enumerate(request.get("tools") or []):
        if not isinstance(tool, dict):
            continue
        value = strip_cache_control(tool)
        blocks.append(
            Block(
                index=len(blocks),
                path=f"tools[{index}]",
                scope="tools",
                role="",
                block_type=str(tool.get("type") or "tool"),
                value={"scope": "tools", "tool": value},
                raw_tokens=estimate_json_tokens(value),
                cache_control=cache_control(tool),
            )
        )

    for index, system_block in enumerate(request.get("system") or []):
        if not isinstance(system_block, dict) or not is_cacheable_block(system_block, "system"):
            continue
        value = strip_cache_control(system_block)
        blocks.append(
            Block(
                index=len(blocks),
                path=f"system[{index}]",
                scope="system",
                role="system",
                block_type=str(system_block.get("type") or "text"),
                value={"scope": "system", "block": value},
                raw_tokens=estimate_json_tokens(value),
                cache_control=cache_control(system_block),
            )
        )

    for message_index, message in enumerate(request.get("messages") or []):
        if not isinstance(message, dict):
            continue
        role = str(message.get("role") or "")
        for content_index, content_block in enumerate(message.get("content") or []):
            if not isinstance(content_block, dict) or not is_cacheable_block(content_block, "messages"):
                continue
            value = strip_cache_control(content_block)
            blocks.append(
                Block(
                    index=len(blocks),
                    path=f"messages[{message_index}].content[{content_index}]",
                    scope="messages",
                    role=role,
                    block_type=str(content_block.get("type") or ""),
                    value={"scope": "messages", "role": role, "block": value},
                    raw_tokens=estimate_json_tokens(value),
                    cache_control=cache_control(content_block),
                )
            )

    for index, block in enumerate(blocks):
        block.index = index
    return blocks


def assign_prefixes(
    request: dict[str, Any],
    blocks: list[Block],
    record: RequestRecord,
    calibrate: bool,
) -> None:
    raw_total = sum(block.raw_tokens for block in blocks)
    observed_cacheable_total = record.actual_read + record.actual_create
    scale = observed_cacheable_total / raw_total if calibrate and raw_total > 0 and observed_cacheable_total > 0 else 1.0

    namespace = {
        "model": request.get("model"),
        "message_salt": {
            "tool_choice": strip_cache_control(request.get("tool_choice")),
            "thinking": strip_cache_control(request.get("thinking")),
        },
    }

    prefix_hash = sha256_json({"namespace": namespace})
    cumulative_raw = 0
    previous_tokens = 0
    for block in blocks:
        cumulative_raw += block.raw_tokens
        block_hash = sha256_json(block.value)
        prefix_hash = hashlib.sha256((prefix_hash + "\x00" + block_hash).encode("utf-8")).hexdigest()
        block.prefix_hash = prefix_hash
        tokens = int(round(cumulative_raw * scale))
        if tokens <= previous_tokens:
            tokens = previous_tokens + 1
        block.prefix_tokens = tokens
        previous_tokens = tokens


def choose_breakpoints(
    *,
    strategy: str,
    request: dict[str, Any],
    blocks: list[Block],
    max_breakpoints: int,
    default_ttl_seconds: int,
) -> list[Breakpoint]:
    if strategy == "none":
        return []
    if strategy == "observed":
        return observed_breakpoints(request, blocks, default_ttl_seconds)
    if strategy == "automatic":
        return [Breakpoint(blocks[-1].index, top_level_ttl(request, default_ttl_seconds), "automatic")]
    if strategy == "tail":
        return tail_breakpoints(blocks, max_breakpoints, default_ttl_seconds)
    if strategy == "spread":
        return spread_breakpoints(blocks, max_breakpoints, default_ttl_seconds)
    raise ValueError(f"unknown strategy {strategy!r}")


def observed_breakpoints(
    request: dict[str, Any],
    blocks: list[Block],
    default_ttl_seconds: int,
) -> list[Breakpoint]:
    breakpoints: list[Breakpoint] = []
    path_to_block = {block.path: block for block in blocks}
    for block in blocks:
        if block.cache_control:
            breakpoints.append(
                Breakpoint(block.index, ttl_from_cache_control(block.cache_control, default_ttl_seconds), "block")
            )
    if cache_control(request):
        breakpoints.append(
            Breakpoint(blocks[-1].index, top_level_ttl(request, default_ttl_seconds), "top_level")
        )
    return dedupe_breakpoints(breakpoints, path_to_block)


def tail_breakpoints(
    blocks: list[Block],
    max_breakpoints: int,
    default_ttl_seconds: int,
) -> list[Breakpoint]:
    selected: list[int] = []
    for scope in ("tools", "system", "messages"):
        indexes = [block.index for block in blocks if block.scope == scope]
        if indexes:
            selected.append(indexes[-1])
    return [
        Breakpoint(index, default_ttl_seconds, "tail")
        for index in dedupe_indexes(selected)[-max_breakpoints:]
    ]


def spread_breakpoints(
    blocks: list[Block],
    max_breakpoints: int,
    default_ttl_seconds: int,
) -> list[Breakpoint]:
    selected: list[int] = []
    for scope in ("tools", "system"):
        indexes = [block.index for block in blocks if block.scope == scope]
        if indexes:
            selected.append(indexes[-1])

    remaining = max(0, max_breakpoints - len(dedupe_indexes(selected)))
    user_message_indexes = [
        block.index for block in blocks if block.scope == "messages" and block.role == "user"
    ]
    selected.extend(evenly_spaced(user_message_indexes, remaining))
    return [
        Breakpoint(index, default_ttl_seconds, "spread")
        for index in dedupe_indexes(selected)[-max_breakpoints:]
    ]


def check_indexes(
    breakpoints: list[Breakpoint],
    blocks: list[Block],
    lookback_blocks: int,
) -> set[int]:
    indexes: set[int] = set()
    max_index = len(blocks) - 1
    for breakpoint in breakpoints:
        start = max(0, breakpoint.index - lookback_blocks)
        end = min(max_index, breakpoint.index)
        indexes.update(range(start, end + 1))
    return indexes


def is_cacheable_block(block: dict[str, Any], scope: str) -> bool:
    if scope == "tools":
        return True
    block_type = str(block.get("type") or "text")
    if block_type == "thinking":
        return False
    if block_type == "text" and not str(block.get("text") or "").strip():
        return False
    if scope == "system":
        return True
    return block_type in CACHEABLE_MESSAGE_TYPES


def cache_control(value: Any) -> dict[str, Any] | None:
    if not isinstance(value, dict):
        return None
    control = value.get("cache_control")
    return control if isinstance(control, dict) else None


def strip_cache_control(value: Any) -> Any:
    value = copy.deepcopy(value)
    if isinstance(value, dict):
        value.pop("cache_control", None)
        return {key: strip_cache_control(inner) for key, inner in value.items()}
    if isinstance(value, list):
        return [strip_cache_control(inner) for inner in value]
    return value


def estimate_json_tokens(value: Any) -> int:
    data = canonical_json(value).encode("utf-8")
    if not data:
        return 0
    base64_bytes = count_base64_bytes(data)
    text_bytes = len(data) - base64_bytes
    return text_bytes // 4 + base64_bytes // 7 + 1


def count_base64_bytes(data: bytes) -> int:
    total = 0
    marker = b'"data":"'
    offset = 0
    while offset < len(data):
        index = data.find(marker, offset)
        if index < 0:
            break
        window_start = max(0, index - 200)
        if b'"media_type"' in data[window_start:index] or b'"type":"base64"' in data[window_start:index]:
            value_start = index + len(marker)
            value_end = data.find(b'"', value_start)
            if value_end > value_start:
                total += value_end - value_start
                offset = value_end + 1
                continue
        offset = index + len(marker)
    return total


def actual_usage(data: dict[str, Any], field: str) -> int:
    events = data.get("anthropic_stream_events")
    if isinstance(events, list):
        for event in events:
            if event.get("type") == "message_start":
                usage = ((event.get("message") or {}).get("usage") or {})
                if field in usage:
                    return int(usage.get(field) or 0)
        for event in events:
            if event.get("type") == "message_delta":
                usage = event.get("usage") or {}
                if field in usage:
                    return int(usage.get(field) or 0)
    usage = (data.get("anthropic_response") or {}).get("usage") or {}
    return int(usage.get(field) or 0)


def resolve_min_cache_tokens(model: str, arg: str) -> int:
    if arg != "auto":
        return int(arg)
    normalized = model.lower().replace("_", "-")
    if any(name in normalized for name in ("mythos", "opus-4-7", "opus-4.7", "opus-4-6", "opus-4.6", "opus-4-5", "opus-4.5", "haiku-4-5", "haiku-4.5")):
        return 4096
    if "sonnet-4-6" in normalized or "sonnet-4.6" in normalized:
        return 2048
    if any(name in normalized for name in ("haiku-3-5", "haiku-3.5", "haiku-3")):
        return 2048
    if any(name in normalized for name in ("sonnet-4-5", "sonnet-4.5", "opus-4-1", "opus-4.1", "opus-4", "sonnet-4", "sonnet-3-7", "sonnet-3.7")):
        return 1024
    return 1024


def parse_ttl(value: str | None) -> int:
    if not value:
        return DEFAULT_TTL_SECONDS
    text = str(value).strip()
    match = re.fullmatch(r"(\d+)([smh])", text)
    if not match:
        raise ValueError(f"invalid ttl {value!r}; expected like 5m or 1h")
    amount = int(match.group(1))
    unit = match.group(2)
    if unit == "s":
        return amount
    if unit == "m":
        return amount * 60
    if unit == "h":
        return amount * 3600
    raise ValueError(f"invalid ttl unit {unit!r}")


def ttl_from_cache_control(control: dict[str, Any] | None, default_ttl_seconds: int) -> int:
    if not control:
        return default_ttl_seconds
    ttl = control.get("ttl")
    return parse_ttl(str(ttl)) if ttl else default_ttl_seconds


def top_level_ttl(request: dict[str, Any], default_ttl_seconds: int) -> int:
    return ttl_from_cache_control(cache_control(request), default_ttl_seconds)


def sha256_json(value: Any) -> str:
    return hashlib.sha256(canonical_json(value).encode("utf-8")).hexdigest()


def canonical_json(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))


def parse_time(value: Any) -> dt.datetime:
    if not value:
        return dt.datetime.min.replace(tzinfo=dt.timezone.utc)
    text = str(value)
    if text.endswith("Z"):
        text = text[:-1] + "+00:00"
    try:
        parsed = dt.datetime.fromisoformat(text)
    except ValueError:
        return dt.datetime.min.replace(tzinfo=dt.timezone.utc)
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=dt.timezone.utc)
    return parsed


def numeric_sort_key(name: str) -> tuple[int, str]:
    stem = Path(name).stem
    return (int(stem), name) if stem.isdigit() else (sys.maxsize, name)


def dedupe_indexes(indexes: list[int]) -> list[int]:
    seen: set[int] = set()
    out: list[int] = []
    for index in indexes:
        if index in seen:
            continue
        seen.add(index)
        out.append(index)
    return out


def dedupe_breakpoints(
    breakpoints: list[Breakpoint],
    path_to_block: dict[str, Block],
) -> list[Breakpoint]:
    del path_to_block
    by_index: dict[int, Breakpoint] = {}
    for breakpoint in breakpoints:
        current = by_index.get(breakpoint.index)
        if current is None or breakpoint.ttl_seconds > current.ttl_seconds:
            by_index[breakpoint.index] = breakpoint
    return [by_index[index] for index in sorted(by_index)]


def evenly_spaced(indexes: list[int], limit: int) -> list[int]:
    if limit <= 0 or not indexes:
        return []
    if limit >= len(indexes):
        return indexes[:]
    selected: list[int] = []
    seen_positions: set[int] = set()
    for slot in range(1, limit + 1):
        position = math.ceil(slot * len(indexes) / limit) - 1
        position = max(0, min(position, len(indexes) - 1))
        if position in seen_positions:
            continue
        seen_positions.add(position)
        selected.append(indexes[position])
    for position, index in enumerate(indexes):
        if len(selected) >= limit:
            break
        if position not in seen_positions:
            selected.append(index)
    return selected


def empty_result(record: RequestRecord) -> SimRequestResult:
    return SimRequestResult(
        request_number=record.request_number,
        read=0,
        create=0,
        input_tokens=0,
        hit_tokens=0,
        breakpoint_tokens=0,
        breakpoint_count=0,
        hit=False,
        actual_read=record.actual_read,
        actual_create=record.actual_create,
        actual_input=record.actual_input,
    )


def print_text_report(
    records: list[RequestRecord],
    summaries: list[SimSummary],
    fit_summary: tuple[int, SimSummary] | None,
) -> None:
    actual_read = sum(record.actual_read for record in records)
    actual_create = sum(record.actual_create for record in records)
    actual_input = sum(record.actual_input for record in records)
    actual_ratio = actual_read / actual_create if actual_create else math.inf

    print(f"trace_requests: {len(records)}")
    print(
        "actual_usage: "
        f"read={actual_read} create={actual_create} input={actual_input} "
        f"read/create={format_ratio(actual_ratio)}"
    )
    print()
    print(
        f"{'strategy':<12} {'requests':>8} {'hits':>8} {'hit%':>7} "
        f"{'read':>12} {'create':>12} {'input':>12} {'read/create':>12} {'bp/req':>8}"
    )
    for summary in summaries:
        bp_per_request = summary.breakpoints / summary.requests if summary.requests else 0.0
        print(
            f"{summary.strategy:<12} {summary.requests:>8} {summary.hits:>8} "
            f"{summary.hit_rate * 100:>6.1f}% {summary.read:>12} {summary.create:>12} "
            f"{summary.input_tokens:>12} {format_ratio(summary.read_create_ratio):>12} "
            f"{bp_per_request:>8.2f}"
        )

    if fit_summary is not None:
        lookback, summary = fit_summary
        print()
        print(
            "fit_lookback: "
            f"lookback_blocks={lookback} "
            f"read={summary.read} create={summary.create} input={summary.input_tokens} "
            f"read/create={format_ratio(summary.read_create_ratio)}"
        )

    if len(summaries) == 1:
        summary = summaries[0]
        print()
        print(
            "sim_minus_actual: "
            f"read={summary.read - actual_read} "
            f"create={summary.create - actual_create} "
            f"input={summary.input_tokens - actual_input}"
        )


def format_ratio(value: float) -> str:
    if math.isinf(value):
        return "inf"
    return f"{value:.2f}x"


def summary_to_json(summary: SimSummary) -> dict[str, Any]:
    return {
        "strategy": summary.strategy,
        "requests": summary.requests,
        "hits": summary.hits,
        "hit_rate": summary.hit_rate,
        "read": summary.read,
        "create": summary.create,
        "input": summary.input_tokens,
        "read_create_ratio": None if math.isinf(summary.read_create_ratio) else summary.read_create_ratio,
        "breakpoints": summary.breakpoints,
        "actual": {
            "read": summary.actual_read,
            "create": summary.actual_create,
            "input": summary.actual_input,
        },
    }


def write_per_request(path: Path, summaries: list[SimSummary]) -> None:
    with path.open("w", encoding="utf-8") as handle:
        for summary in summaries:
            for result in summary.per_request:
                handle.write(
                    json.dumps(
                        {
                            "strategy": summary.strategy,
                            **dataclasses.asdict(result),
                        },
                        ensure_ascii=False,
                        separators=(",", ":"),
                    )
                    + "\n"
                )


if __name__ == "__main__":
    raise SystemExit(main())
