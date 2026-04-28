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
      moonbridge:
        to: "deepseek/deepseek-v4-pro"

Old DeepSeek V4 extension format (global):
  provider:
    deepseek_v4: true

Intermediate format (provider-level, also migrated):
  provider:
    providers:
      deepseek:
        deepseek_v4: true

New format (extension slot):
  extensions:
    deepseek_v4:
      config:
        reinforce_instructions: true
  provider:
    providers:
      deepseek:
        models:
          deepseek-v4-pro:
            extensions:
              deepseek_v4:
                enabled: true

Old Visual extension formats (migrated):
  provider:
    visual: true

  provider:
    providers:
      deepseek:
        visual: true

New Visual extension format:
  extensions:
    visual:
      config:
        provider: kimi
        model: kimi-for-coding
        max_rounds: 4
        max_tokens: 2048
  provider:
    providers:
      deepseek:
        models:
          deepseek-v4-pro:
            extensions:
              visual:
                enabled: true

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


DEEPSEEK_EXTENSION = "deepseek_v4"
VISUAL_EXTENSION = "visual"
VISUAL_MODEL_FLAG = "enable_visual_extension"
LEGACY_VISUAL_MODEL_FLAG = "visual"


def needs_migration(provider_block: dict) -> bool:
    """Return True if the provider block still has any obsolete shape."""
    if "deepseek_v4" in provider_block:
        return True
    if needs_provider_level_deepseek_v4_migration(provider_block):
        return True
    if needs_visual_migration(provider_block):
        return True
    if needs_route_migration(provider_block):
        return True
    return needs_model_migration(provider_block)


def needs_root_migration(data: dict) -> bool:
    plugins = data.get("plugins")
    return isinstance(plugins, dict) and bool(plugins)


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
        models = pdef.get("models") if isinstance(pdef, dict) else None
        for mdef in (models or {}).values():
            if isinstance(mdef, dict) and "deepseek_v4" in mdef:
                return True
    return False


def needs_route_migration(provider_block: dict) -> bool:
    routes = provider_block.get("routes") or {}
    return any(isinstance(route, str) for route in routes.values())


def needs_visual_migration(provider_block: dict) -> bool:
    """Return True if Visual still uses an obsolete shape or flag name."""
    visual = provider_block.get("visual")
    if visual is not None and not isinstance(visual, dict):
        return True
    if isinstance(visual, dict):
        return True

    providers = provider_block.get("providers")
    if not providers:
        return False
    for pdef in providers.values():
        if isinstance(pdef, dict) and "visual" in pdef:
            return True
    if has_legacy_model_level_visual(providers):
        return True
    if has_old_visual_model_flag(providers):
        return True
    return False


def migrate(data: dict) -> dict:
    """Transform the config dict in-place from old to new format."""
    provider_block = data.get("provider")
    if not provider_block:
        migrate_plugin_configs(data)
        return data

    if not needs_migration(provider_block) and not needs_root_migration(data):
        print("Config already uses the current format. Nothing to do.")
        return data

    migrate_models = needs_model_migration(provider_block)
    providers = provider_block.get("providers") or {}
    migrate_plugin_configs(data)
    migrate_deepseek_v4(data, provider_block, providers)
    migrate_visual(data, provider_block, providers)
    migrate_routes(provider_block)
    if not migrate_models:
        return data

    routes: dict[str, dict] = {}

    for provider_key, pdef in providers.items():
        old_models = pdef.get("models")
        if not old_models:
            continue

        new_models: dict = {}
        for alias, mdef in old_models.items():
            if not isinstance(mdef, dict):
                # Bare value or empty -- treat alias as upstream name.
                new_models[alias] = mdef
                routes[alias] = {"to": f"{provider_key}/{alias}"}
                continue

            upstream_name = mdef.pop("name", None)
            if not upstream_name:
                # No "name" field -- alias IS the upstream name (already new format).
                new_models[alias] = mdef
                routes[alias] = {"to": f"{provider_key}/{alias}"}
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
            routes[alias] = {"to": f"{provider_key}/{upstream_name}"}

        pdef["models"] = new_models

    # Merge with any existing routes (shouldn't exist in old format, but be safe).
    existing_routes = provider_block.get("routes", {})
    if existing_routes:
        for k, v in routes.items():
            existing_routes.setdefault(k, v)
    else:
        provider_block["routes"] = routes

    migrate_routes(provider_block)

    return data


def ensure_extension_block(data: dict, name: str) -> dict:
    extensions = data.setdefault("extensions", {})
    return extensions.setdefault(name, {})


def ensure_extension_config(data: dict, name: str) -> dict:
    block = ensure_extension_block(data, name)
    return block.setdefault("config", {})


def set_model_extension_enabled(models: dict, model_name: str, mdef: object, name: str, enabled: bool = True) -> None:
    if mdef is None:
        mdef = {}
        models[model_name] = mdef
    if not isinstance(mdef, dict):
        return
    extensions = mdef.setdefault("extensions", {})
    extensions.setdefault(name, {})["enabled"] = enabled


def migrate_plugin_configs(data: dict) -> None:
    plugins = data.get("plugins")
    if not isinstance(plugins, dict):
        return
    for name, cfg in plugins.items():
        if not isinstance(cfg, dict):
            continue
        target = ensure_extension_config(data, str(name))
        for key, value in cfg.items():
            target.setdefault(key, value)
    data.pop("plugins", None)


def migrate_routes(provider_block: dict) -> None:
    routes = provider_block.get("routes")
    if not isinstance(routes, dict):
        return
    for alias, route in list(routes.items()):
        if isinstance(route, str):
            routes[alias] = {"to": route}


def migrate_deepseek_v4(data: dict, provider_block: dict, providers: dict) -> None:
    """Migrate deepseek_v4 from global/provider level to model level.

    Handles three source locations:
    1. provider.deepseek_v4 (global, oldest format)
    2. provider.providers.<key>.deepseek_v4 (intermediate format)
    Both are migrated to provider.providers.<key>.models.<name>.extensions.deepseek_v4.enabled.
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
                    "provider could be identified. Enable extensions.deepseek_v4 "
                    "under the right model entries manually.",
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
                f"no models defined. Add extensions.deepseek_v4.enabled to model entries manually.",
                file=sys.stderr,
            )
            continue
        for model_name, mdef in models.items():
            set_model_extension_enabled(models, model_name, mdef, DEEPSEEK_EXTENSION, True)

    # Step 3: Rename any existing model-level deepseek_v4 flags.
    for pdef in providers.values():
        if not isinstance(pdef, dict):
            continue
        models = pdef.get("models") or {}
        for model_name, mdef in models.items():
            if not isinstance(mdef, dict) or "deepseek_v4" not in mdef:
                continue
            enabled = boolish(mdef.pop("deepseek_v4"))
            set_model_extension_enabled(models, model_name, mdef, DEEPSEEK_EXTENSION, enabled)


def migrate_visual(data: dict, provider_block: dict, providers: dict) -> None:
    """Migrate Visual enablement to extensions.visual config + model-level flags."""
    visual = provider_block.pop("visual", None)
    global_visual_enabled = False
    if visual is not None and not isinstance(visual, dict):
        enabled = boolish(visual)
        global_visual_enabled = enabled
        visual = {"enabled": enabled}

    enabled_provider_keys: set[str] = set()
    for key, pdef in providers.items():
        if not isinstance(pdef, dict):
            continue
        if "visual" in pdef:
            enabled = boolish(pdef.pop("visual"))
            if enabled:
                enabled_provider_keys.add(key)

    for key in enabled_provider_keys:
        pdef = providers.get(key)
        if not isinstance(pdef, dict):
            continue
        models = pdef.get("models")
        if not models:
            print(
                f"Warning: visual enabled for provider {key!r}, but it has "
                f"no models defined. Add {VISUAL_MODEL_FLAG}: true to model entries manually.",
                file=sys.stderr,
            )
            continue
        for model_name, mdef in models.items():
            enable_visual_extension(models, model_name, mdef)

    migrate_model_visual_flags(providers)

    visual_enabled = bool(enabled_provider_keys) or has_visual_extension_model(providers)
    if visual_enabled and visual is None:
        visual = {"enabled": True}

    if isinstance(visual, dict) and (boolish(visual.get("enabled")) or visual_enabled):
        fill_visual_provider_model(visual, providers)
        cfg = ensure_extension_config(data, VISUAL_EXTENSION)
        for key in ("provider", "model", "max_rounds", "max_tokens"):
            if key in visual and key != "enabled":
                cfg.setdefault(key, visual[key])
        cfg.setdefault("max_rounds", 4)
        cfg.setdefault("max_tokens", 2048)
        if global_visual_enabled:
            mark_global_visual_models(providers, str(visual.get("provider", "")).strip())


def has_visual_extension_model(providers: dict) -> bool:
    """Return True if any provider model already opts in to Visual."""
    for pdef in providers.values():
        if not isinstance(pdef, dict):
            continue
        models = pdef.get("models") or {}
        for mdef in models.values():
            if not isinstance(mdef, dict):
                continue
            if boolish(mdef.get(VISUAL_MODEL_FLAG)):
                return True
            ext = (mdef.get("extensions") or {}).get(VISUAL_EXTENSION)
            if isinstance(ext, dict) and boolish(ext.get("enabled")):
                return True
    return False


def has_legacy_model_level_visual(providers: dict) -> bool:
    """Return True if any provider model still uses the old Visual flag name."""
    for pdef in providers.values():
        if not isinstance(pdef, dict):
            continue
        models = pdef.get("models") or {}
        for mdef in models.values():
            if isinstance(mdef, dict) and LEGACY_VISUAL_MODEL_FLAG in mdef:
                return True
    return False


def has_old_visual_model_flag(providers: dict) -> bool:
    for pdef in providers.values():
        if not isinstance(pdef, dict):
            continue
        models = pdef.get("models") or {}
        for mdef in models.values():
            if isinstance(mdef, dict) and VISUAL_MODEL_FLAG in mdef:
                return True
    return False


def migrate_model_visual_flags(providers: dict) -> None:
    """Rename model-level visual flags to extensions.visual.enabled."""
    for pdef in providers.values():
        if not isinstance(pdef, dict):
            continue
        models = pdef.get("models") or {}
        for mdef in models.values():
            if not isinstance(mdef, dict) or LEGACY_VISUAL_MODEL_FLAG not in mdef:
                continue
            legacy_value = mdef.pop(LEGACY_VISUAL_MODEL_FLAG)
            extensions = mdef.setdefault("extensions", {})
            extensions.setdefault(VISUAL_EXTENSION, {})["enabled"] = boolish(legacy_value)
            if VISUAL_MODEL_FLAG in mdef:
                current_value = mdef.pop(VISUAL_MODEL_FLAG)
                extensions.setdefault(VISUAL_EXTENSION, {})["enabled"] = boolish(current_value)
    for pdef in providers.values():
        if not isinstance(pdef, dict):
            continue
        models = pdef.get("models") or {}
        for mdef in models.values():
            if not isinstance(mdef, dict) or VISUAL_MODEL_FLAG not in mdef:
                continue
            value = mdef.pop(VISUAL_MODEL_FLAG)
            extensions = mdef.setdefault("extensions", {})
            extensions.setdefault(VISUAL_EXTENSION, {})["enabled"] = boolish(value)


def enable_visual_extension(models: dict, model_name: str, mdef: object) -> None:
    """Mark a model as Visual-enabled using the current flag name."""
    set_model_extension_enabled(models, model_name, mdef, VISUAL_EXTENSION, True)


def mark_global_visual_models(providers: dict, visual_provider_key: str) -> None:
    """Migrate old global Visual enablement to all non-Kimi Anthropic models."""
    marked = 0
    for key, pdef in providers.items():
        if not isinstance(pdef, dict) or not provider_uses_anthropic_protocol(pdef):
            continue
        if key == visual_provider_key or provider_looks_like_kimi(key, pdef):
            continue
        models = pdef.get("models")
        if not models:
            continue
        for model_name, mdef in models.items():
            if mdef is None:
                models[model_name] = {"extensions": {VISUAL_EXTENSION: {"enabled": True}}}
                marked += 1
            elif isinstance(mdef, dict):
                mdef.setdefault("extensions", {}).setdefault(VISUAL_EXTENSION, {})["enabled"] = True
                marked += 1
    if marked == 0:
        print(
            "Warning: provider.visual was true, but no non-Kimi Anthropic "
            "models were found. Add extensions.visual.enabled to target model entries manually.",
            file=sys.stderr,
        )


def fill_visual_provider_model(visual: dict, providers: dict) -> None:
    """Fill provider.visual.provider/model when a Kimi provider can be inferred."""
    provider = str(visual.get("provider", "")).strip()
    model = str(visual.get("model", "")).strip()
    inferred_provider, inferred_model = infer_visual_provider_model(providers, provider)

    if not provider and inferred_provider:
        visual["provider"] = inferred_provider
        provider = inferred_provider
    if not model and inferred_model:
        visual["model"] = inferred_model
        model = inferred_model

    missing: list[str] = []
    if not provider:
        missing.append("provider")
    if not model:
        missing.append("model")
    if missing:
        print(
            "Warning: Visual was enabled, but provider.visual."
            + " and provider.visual.".join(missing)
            + " could not be inferred. Fill these fields manually.",
            file=sys.stderr,
        )


def infer_visual_provider_model(providers: dict, preferred_provider: str = "") -> tuple[str, str]:
    """Infer the Kimi provider key and model name for Visual forwarding."""
    if preferred_provider:
        pdef = providers.get(preferred_provider)
        if isinstance(pdef, dict) and provider_uses_anthropic_protocol(pdef):
            return preferred_provider, infer_visual_model_name(pdef)

    candidates: list[tuple[str, dict]] = []
    for key, pdef in providers.items():
        if not isinstance(pdef, dict) or not provider_uses_anthropic_protocol(pdef):
            continue
        if provider_looks_like_kimi(key, pdef):
            candidates.append((key, pdef))

    if not candidates:
        return "", ""
    for key, pdef in candidates:
        if key.strip().lower() == "kimi":
            return key, infer_visual_model_name(pdef)
    key, pdef = candidates[0]
    return key, infer_visual_model_name(pdef)


def infer_visual_model_name(pdef: dict) -> str:
    models = pdef.get("models") or {}
    if not models:
        return ""
    if "kimi-for-coding" in models:
        return "kimi-for-coding"
    for name in models:
        if "kimi" in str(name).lower() or "vision" in str(name).lower():
            return str(name)
    if len(models) == 1:
        return str(next(iter(models)))
    return str(next(iter(models)))


def provider_looks_like_kimi(provider_key: str, pdef: dict) -> bool:
    values = [provider_key, str(pdef.get("base_url", ""))]
    models = pdef.get("models") or {}
    for model_key, model_def in models.items():
        values.append(str(model_key))
        if isinstance(model_def, dict):
            values.append(str(model_def.get("name", "")))
    return any("kimi" in value.lower() or "moonshot" in value.lower() for value in values)


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

    with open(input_path) as f:
        data = yaml.load(f)

    if data is None:
        print(f"Error: {input_path} is empty or invalid YAML.", file=sys.stderr)
        sys.exit(1)

    migrate(data)

    if args.dry_run:
        yaml.dump(data, sys.stdout)
    else:
        with open(output_path, "w") as f:
            yaml.dump(data, f)
        print(f"Migrated config written to {output_path}")


if __name__ == "__main__":
    main()
