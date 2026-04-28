#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["httpx", "rich"]
# ///
"""Analyze sub2api / MoonBridge cache usage with a terminal-adaptive table.

Usage:
  BASE_URL=http://... API_KEY=sk-... python3 scripts/sub2api_cache_analyze.py
  # or with uv:
  BASE_URL=http://... API_KEY=sk-... uv run scripts/sub2api_cache_analyze.py
"""

from __future__ import annotations

import os
import sys

import httpx
from rich.console import Console
from rich.table import Table
from rich.text import Text


def fetch_usage(base_url: str, api_key: str) -> list[dict]:
    url = f"{base_url.rstrip('/')}/v1/usage"
    resp = httpx.get(url, headers={"Authorization": f"Bearer {api_key}"}, timeout=15)
    resp.raise_for_status()
    return resp.json().get("model_stats", [])


def cache_hit_rate(input_tokens: int, cache_read: int) -> float:
    total = input_tokens + cache_read
    return (cache_read / total * 100) if total > 0 else 0.0


def cache_rw_ratio(cache_read: int, cache_write: int) -> float | None:
    return (cache_read / cache_write) if cache_write > 0 else None


def fmt_tokens(n: int) -> str:
    if n >= 1_000_000:
        return f"{n / 1_000_000:.1f}M"
    if n >= 1_000:
        return f"{n / 1_000:.1f}K"
    return str(n)


def fmt_cost(v: float) -> str:
    return f"{v:.4f}" if v else "0"


def fmt_pct(v: float) -> Text:
    s = f"{v:.1f}%"
    if v >= 80:
        return Text(s, style="bold green")
    if v >= 40:
        return Text(s, style="yellow")
    return Text(s, style="red")


def fmt_ratio(v: float | None) -> str:
    return f"{v:.2f}" if v is not None else "N/A"


def cost_per_mtok(cost: float, total_tokens: int) -> float | None:
    """Cost per million tokens."""
    return (cost / total_tokens * 1_000_000) if total_tokens > 0 else None


def fmt_unit_cost(v: float | None) -> Text:
    if v is None:
        return Text("N/A", style="dim")
    s = f"¥{v:.2f}"
    if v <= 1:
        return Text(s, style="bold green")
    if v <= 5:
        return Text(s, style="yellow")
    return Text(s, style="red")


def build_table(stats: list[dict]) -> Table:
    table = Table(
        title="Cache Usage",
        expand=True,
        show_lines=False,
        pad_edge=True,
    )
    table.add_column("Model", style="cyan", no_wrap=True)
    table.add_column("Total", justify="right")
    table.add_column("Input", justify="right")
    table.add_column("Cache Read", justify="right", style="green")
    table.add_column("Cache Write", justify="right", style="dim")
    table.add_column("Output", justify="right")
    table.add_column("Cost", justify="right", style="yellow")
    table.add_column("Hit Rate", justify="right")
    table.add_column("R/W Ratio", justify="right")
    table.add_column("¥/MTok", justify="right")

    totals = dict(total=0, inp=0, cr=0, cw=0, out=0, cost=0.0)

    for s in sorted(stats, key=lambda x: x.get("cost", 0), reverse=True):
        total = s.get("total_tokens", 0)
        inp = s.get("input_tokens", 0)
        cr = s.get("cache_read_tokens", 0)
        cw = s.get("cache_creation_tokens", 0)
        out = s.get("output_tokens", 0)
        cost = s.get("cost", 0) or 0

        totals["total"] += total
        totals["inp"] += inp
        totals["cr"] += cr
        totals["cw"] += cw
        totals["out"] += out
        totals["cost"] += cost

        hit = cache_hit_rate(inp, cr)
        rw = cache_rw_ratio(cr, cw)
        unit = cost_per_mtok(cost, total)

        table.add_row(
            s.get("model", "?"),
            fmt_tokens(total),
            fmt_tokens(inp),
            fmt_tokens(cr),
            fmt_tokens(cw),
            fmt_tokens(out),
            fmt_cost(cost),
            fmt_pct(hit),
            fmt_ratio(rw),
            fmt_unit_cost(unit),
        )

    if len(stats) > 1:
        hit = cache_hit_rate(totals["inp"], totals["cr"])
        rw = cache_rw_ratio(totals["cr"], totals["cw"])
        unit = cost_per_mtok(totals["cost"], totals["total"])
        table.add_section()
        table.add_row(
            Text("TOTAL", style="bold"),
            fmt_tokens(totals["total"]),
            fmt_tokens(totals["inp"]),
            fmt_tokens(totals["cr"]),
            fmt_tokens(totals["cw"]),
            fmt_tokens(totals["out"]),
            fmt_cost(totals["cost"]),
            fmt_pct(hit),
            fmt_ratio(rw),
            fmt_unit_cost(unit),
        )

    return table


def main() -> None:
    base_url = os.environ.get("BASE_URL", "")
    api_key = os.environ.get("API_KEY", "")
    if not base_url or not api_key:
        print("Error: set BASE_URL and API_KEY environment variables.", file=sys.stderr)
        sys.exit(1)

    console = Console()
    try:
        stats = fetch_usage(base_url, api_key)
    except httpx.HTTPStatusError as e:
        console.print(f"[red]HTTP {e.response.status_code}:[/] {e.response.text}")
        sys.exit(1)
    except httpx.ConnectError as e:
        console.print(f"[red]Connection failed:[/] {e}")
        sys.exit(1)

    if not stats:
        console.print("[dim]No usage data.[/]")
        return

    console.print(build_table(stats))


if __name__ == "__main__":
    main()
