"""Export an exact public exchange from a completed demo into a new directory."""

import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import sys

sys.dont_write_bytecode = True

from demo import ROOT, ENGINE, call, public_files


def manifest(files):
    return "".join(f"{hashlib.sha256(data).hexdigest()}  {path}\n"
                   for path, data in sorted(files.items()))


def export(directory, destination):
    exchange = directory / "exchange"
    if not (directory / "index.json").is_file():
        raise ValueError("Complete make demo before exporting its reference files.")
    files = public_files(exchange)
    call(ENGINE, "validate", "--exchange-dir", exchange, *files)
    sources = {str(path.relative_to(ROOT)): path.read_bytes()
               for folder in ("cmd", "internal") for path in (ROOT / folder).rglob("*.go")}
    sources.update({name: (ROOT / name).read_bytes() for name in ("go.mod", "go.sum")})
    provenance = {
        "reference_version": 1,
        "contract_version": 1,
        "generation_command": "make demo",
        "producer_source_sha256": hashlib.sha256(manifest(sources).encode()).hexdigest(),
        "source_hash_rule": "SHA-256 of sorted SHA256SUMS lines for go.mod, go.sum, cmd/**/*.go, and internal/**/*.go",
        "volatile_fields": ["result.attempt_id", "result.timing", "event.attempt_id",
                            "event.event_id", "event.created_at_ms", "event.result.sha256"],
    }
    files["SOURCE.json"] = (json.dumps(provenance, indent=2) + "\n").encode()
    destination.mkdir(mode=0o700)  # Never replace a reviewed bundle or another experiment.
    for name, data in files.items():
        path = destination / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
    (destination / "SHA256SUMS").write_text(manifest(files))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("destination", type=Path, help="A new directory under an existing parent.")
    args = parser.parse_args()
    try:
        export(ROOT / ".yamata/demo", args.destination)
        print("Public reference exported. Review it before replacing a versioned example.")
    except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired) as error:
        parser.exit(1, f"Reference export failed: {error}\n")
