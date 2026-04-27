#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["ruamel.yaml"]
# ///
"""Migrate MoonBridge config.yml to the current provider/routes format.

Old model format (per-provider):
  provider:
    providers:
      deepseek:
        models:
          moonbridge:            # alias as key
            name: deepseek-v4-pro  # upstream model name
            context_window: 1000000
            pricing:
              input_price: 2

New format:
  provider:
    providers:
      deepseek:
        models:
          deepseek-v4-pro:       # upstream model name as key, no "name" field
            context_window: 1000000
            display_name: "DeepSeek V4 Pro"
            description: "Reasoning model with extended thinking."
            default_reasoning_level: "medium"
            supported_reasoning_levels:
              - effort: "low"
                description: "Fast responses with lighter reasoning"
              - effort: "high"
                description: "Greater reasoning depth"
            supports_reasoning_summaries: true
            default_reasoning_summary: "auto"
            pricing:
              input_price: 2
    routes:
      moonbridge: "deepseek/deepseek-v4-pro"

Old DeepSeek V4 extension format (global):
  provider:
    deepseek_v4: true

Intermediate format (provider-level, also migrated):
  provider:
    providers:
      deepseek:
        deepseek_v4: true

New format (model-level):
  provider:
    providers:
      deepseek:
        models:
          deepseek-v4-pro:
            deepseek_v4: true

Usage:
  python3 scripts/migrate_config.py                     # reads config.yml, writes config.yml
  python3 scripts/migrate_config.py old.yml             # reads old.yml, writes old.yml
  python3 scripts/migrate_config.py old.yml new.yml     # reads old.yml, writes new.yml
  python3 scripts/migrate_config.py --dry-run old.yml   # preview without writing
"""

from __future__ import annotations

import argparse
import copy
import sys
from pathlib import Path

from ruamel.yaml import YAML


def needs_migration(provider_block: dict) -> bool:
    """Return True if the provider block still has any obsolete shape."""
    if "deepseek_v4" in provider_block:
        return True
    if needs_provider_level_deepseek_v4_migration(provider_block):
        return True
    return needs_model_migration(provider_block)


def needs_model_migration(provider_block: dict) -> bool:
    """Return True if any provider model entry still uses the old 'name' field."""
    providers = provider_block.get("providers")
    if not providers:
        return False
    for pdef in providers.values():
        models = pdef.get("models")
        if not models:
            continue
        for mdef in models.values():
            if isinstance(mdef, dict) and "name" in mdef:
                return True
    return False


def needs_provider_level_deepseek_v4_migration(provider_block: dict) -> bool:
    """Return True if any provider still has deepseek_v4 at the provider level."""
    providers = provider_block.get("providers")
    if not providers:
        return False
    for pdef in providers.values():
        if isinstance(pdef, dict) and "deepseek_v4" in pdef:
            return True
    return False


def migrate(data: dict) -> dict:
    """Transform the config dict in-place from old to new format."""
    provider_block = data.get("provider")
    if not provider_block:
        return data

    if not needs_migration(provider_block):
        print("Config already uses the current format. Nothing to do.")
        return data

    migrate_models = needs_model_migration(provider_block)
    providers = provider_block.get("providers") or {}
    migrate_deepseek_v4(provider_block, providers)
    if not migrate_models:
        return data

    routes: dict[str, str] = {}

    for provider_key, pdef in providers.items():
        old_models = pdef.get("models")
        if not old_models:
            continue

        new_models: dict = {}
        for alias, mdef in old_models.items():
            if not isinstance(mdef, dict):
                # Bare value or empty -- treat alias as upstream name.
                new_models[alias] = mdef
                routes[alias] = f"{provider_key}/{alias}"
                continue

            upstream_name = mdef.pop("name", None)
            if not upstream_name:
                # No "name" field -- alias IS the upstream name (already new format).
                new_models[alias] = mdef
                routes[alias] = f"{provider_key}/{alias}"
                continue

            # Migrate: alias -> upstream_name, strip "name" field.
            # If multiple aliases point to the same upstream model, merge metadata
            # (last-write-wins for simplicity).
            cleaned = copy.deepcopy(mdef)
            if upstream_name in new_models:
                # Merge: keep existing metadata, overlay new.
                existing = new_models[upstream_name]
                if isinstance(existing, dict):
                    existing.update({k: v for k, v in cleaned.items() if v})
                    cleaned = existing
            new_models[upstream_name] = cleaned if cleaned else {}
            routes[alias] = f"{provider_key}/{upstream_name}"

        pdef["models"] = new_models

    # Merge with any existing routes (shouldn't exist in old format, but be safe).
    existing_routes = provider_block.get("routes", {})
    if existing_routes:
        for k, v in routes.items():
            existing_routes.setdefault(k, v)
    else:
        provider_block["routes"] = routes

    return data


def migrate_deepseek_v4(provider_block: dict, providers: dict) -> None:
    """Migrate deepseek_v4 from global/provider level to model level.

    Handles three source locations:
    1. provider.deepseek_v4 (global, oldest format)
    2. provider.providers.<key>.deepseek_v4 (intermediate format)
    Both are migrated to provider.providers.<key>.models.<name>.deepseek_v4.
    """
    # Step 1: Collect provider keys that should have deepseek_v4 enabled.
    enabled_provider_keys: set[str] = set()

    # From global level.
    if "deepseek_v4" in provider_block:
        enabled = boolish(provider_block.pop("deepseek_v4"))
        if enabled:
            keys = deepseek_provider_candidates(providers)
            if not keys:
                print(
                    "Warning: provider.deepseek_v4 was true, but no DeepSeek-like "
                    "provider could be identified. Add deepseek_v4: true under the "
                    "right model entries manually.",
                    file=sys.stderr,
                )
            else:
                enabled_provider_keys.update(keys)

    # From provider level.
    for key, pdef in providers.items():
        if not isinstance(pdef, dict):
            continue
        if "deepseek_v4" in pdef:
            enabled = boolish(pdef.pop("deepseek_v4"))
            if enabled:
                enabled_provider_keys.add(key)

    # Step 2: Push deepseek_v4 down to each model under the enabled providers.
    for key in enabled_provider_keys:
        pdef = providers.get(key)
        if not isinstance(pdef, dict):
            continue
        models = pdef.get("models")
        if not models:
            print(
                f"Warning: deepseek_v4 enabled for provider {key!r}, but it has "
                f"no models defined. Add deepseek_v4: true to model entries manually.",
                file=sys.stderr,
            )
            continue
        for model_name, mdef in models.items():
            if mdef is None:
                models[model_name] = {"deepseek_v4": True}
            elif isinstance(mdef, dict):
                if "deepseek_v4" not in mdef:
                    mdef["deepseek_v4"] = True
            # else: scalar value, skip (unusual)


def deepseek_provider_candidates(providers: dict) -> list[str]:
    """Infer which provider definitions should receive deepseek_v4."""
    if not providers:
        return []

    candidates = [
        key
        for key, pdef in providers.items()
        if isinstance(pdef, dict)
        if provider_uses_anthropic_protocol(pdef) and provider_looks_like_deepseek(key, pdef)
    ]
    if candidates:
        return candidates

    anthropic_keys = [
        key
        for key, pdef in providers.items()
        if isinstance(pdef, dict) and provider_uses_anthropic_protocol(pdef)
    ]
    if len(anthropic_keys) == 1:
        return anthropic_keys
    if (
        isinstance(providers.get("default"), dict)
        and provider_uses_anthropic_protocol(providers["default"])
    ):
        return ["default"]
    return []


def boolish(value: object) -> bool:
    if isinstance(value, str):
        return value.strip().lower() not in ("", "0", "false", "no", "off")
    return bool(value)


def provider_uses_anthropic_protocol(pdef: dict) -> bool:
    return str(pdef.get("protocol", "")).strip().lower() in ("", "anthropic")


def provider_looks_like_deepseek(provider_key: str, pdef: dict) -> bool:
    values = [provider_key, str(pdef.get("base_url", ""))]
    models = pdef.get("models") or {}
    for model_key, model_def in models.items():
        values.append(str(model_key))
        if isinstance(model_def, dict):
            values.append(str(model_def.get("name", "")))
    return any("deepseek" in value.lower() for value in values)


def main() -> None:
    parser = argparse.ArgumentParser(description="Migrate MoonBridge config to new routes format.")
    parser.add_argument("input", nargs="?", default="config.yml", help="Input config file (default: config.yml)")
    parser.add_argument("output", nargs="?", default=None, help="Output file (default: overwrite input)")
    parser.add_argument("--dry-run", action="store_true", help="Print result to stdout without writing")
    args = parser.parse_args()

    input_path = Path(args.input)
    output_path = Path(args.output) if args.output else input_path

    if not input_path.exists():
        print(f"Error: {input_path} not found.", file=sys.stderr)
        sys.exit(1)

    yaml = YAML()
    yaml.preserve_quotes = True
    yaml.width = 4096  # Avoid unwanted line wrapping.

    with open(input_path, encoding="utf-8") as f:
        data = yaml.load(f)

    if data is None:
        print(f"Error: {input_path} is empty or invalid YAML.", file=sys.stderr)
        sys.exit(1)

    migrate(data)

    if args.dry_run:
        yaml.dump(data, sys.stdout)
    else:
        with open(output_path, "w", encoding="utf-8") as f:
            yaml.dump(data, f)
        print(f"Migrated config written to {output_path}")


if __name__ == "__main__":
    main()
