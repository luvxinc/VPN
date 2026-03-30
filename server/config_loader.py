"""Loads config.yaml and exposes a typed config object."""
import os
from pathlib import Path
import yaml


def _find_config() -> Path:
    candidates = [
        Path(__file__).parent / "config.yaml",
        Path.home() / ".config" / "weiai" / "server_config.yaml",
    ]
    for p in candidates:
        if p.exists():
            return p
    raise FileNotFoundError("config.yaml not found. Copy config.example.yaml → config.yaml and fill in values.")


def load() -> dict:
    path = os.environ.get("WEIAI_CONFIG")
    if path:
        cfg_path = Path(path)
    else:
        cfg_path = _find_config()
    with open(cfg_path) as f:
        return yaml.safe_load(f)


# Module-level singleton
_cfg: dict | None = None


def get() -> dict:
    global _cfg
    if _cfg is None:
        _cfg = load()
    return _cfg


def reset():
    """For testing only."""
    global _cfg
    _cfg = None
