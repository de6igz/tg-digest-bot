#!/usr/bin/env python3
"""Export Telethon session bundles for the Go MTProto importer."""

from __future__ import annotations

import argparse
import json
import pathlib
import sys
from typing import Any, Dict

from telethon.sessions import SQLiteSession, StringSession
from telethon.sync import TelegramClient


def load_metadata(path: pathlib.Path) -> Dict[str, Any]:
    with path.open("r", encoding="utf-8") as fh:
        return json.load(fh)


def detect_name(meta: Dict[str, Any], session_path: pathlib.Path) -> str:
    for key in ("name", "session_file", "phone", "username"):
        value = meta.get(key)
        if isinstance(value, str) and value.strip():
            return value.strip()
    return session_path.stem


def main() -> None:
    parser = argparse.ArgumentParser(
        description=(
            "Extract API credentials and MTProto session data from Telethon"
            " JSON/SQLite files and write a JSON bundle for the Go importer."
        )
    )
    parser.add_argument(
        "--metadata",
        required=True,
        type=pathlib.Path,
        help="Path to Telethon JSON metadata (e.g. 123456789.json)",
    )
    parser.add_argument(
        "--session",
        required=True,
        type=pathlib.Path,
        help="Path to Telethon SQLite session (e.g. 123456789.session)",
    )
    parser.add_argument(
        "--output",
        required=True,
        type=pathlib.Path,
        help="Where to write the resulting JSON bundle",
    )
    parser.add_argument(
        "--name",
        type=str,
        default="",
        help="Optional override for the MTProto account name",
    )
    parser.add_argument(
        "--pool",
        type=str,
        default="default",
        help="Pool name stored in the JSON bundle (default: default)",
    )
    parser.add_argument(
        "--api-id",
        type=int,
        default=None,
        help="Override api_id value (defaults to metadata.app_id)",
    )
    parser.add_argument(
        "--api-hash",
        type=str,
        default=None,
        help="Override api_hash value (defaults to metadata.app_hash)",
    )

    args = parser.parse_args()

    metadata = load_metadata(args.metadata)
    api_id = args.api_id if args.api_id is not None else metadata.get("app_id")
    api_hash = args.api_hash if args.api_hash is not None else metadata.get("app_hash")

    if not api_id or not api_hash:
        raise SystemExit("metadata must include app_id and app_hash (or pass overrides)")

    session_name = args.name.strip() or detect_name(metadata, args.session)

    with TelegramClient(SQLiteSession(str(args.session)), api_id, api_hash) as client:
        string_session = StringSession.save(client.session)

    bundle: Dict[str, Any] = {
        "name": session_name,
        "pool": args.pool.strip() or "default",
        "api_id": api_id,
        "api_hash": api_hash,
        "phone": metadata.get("phone"),
        "username": metadata.get("username"),
        "string_session": string_session,
        "metadata": metadata,
    }

    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("w", encoding="utf-8") as fh:
        json.dump(bundle, fh, ensure_ascii=False, indent=2)
        fh.write("\n")


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:  # pragma: no cover - CLI surface
        print(f"error: {exc}", file=sys.stderr)
        raise
