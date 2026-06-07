"""
foldermcp — An MCP stdio server that lists and reads files from a designated
root folder and its sub-directories.

Usage:
    python foldermcp.py --root <folder-path>

Or run via `uv run` / `pip` with the `mcp` CLI:
    mcp run foldermcp.py --root <folder-path>
"""

import argparse
import fnmatch
import os
import sys
from pathlib import Path
from typing import Optional

from mcp.server.fastmcp import FastMCP

# ── globals ───────────────────────────────────────────────────────────────────

mcp = FastMCP("foldermcp")
root_folder: str = ""

# ── path helpers ──────────────────────────────────────────────────────────────

def safe_resolve(rel_path: str) -> Path:
    """Resolve *rel_path* inside *root_folder* with traversal protection.

    Returns the resolved ``Path`` on success, or calls ``sys.exit(1)`` on
    failure — FastMCP tool handlers return string results, so we embed the
    error message in the return value via the callee.
    """
    # Reject absolute paths and traversal attempts early.
    clean = os.path.normpath(rel_path)
    if os.path.isabs(clean) or clean.startswith(".."):
        raise ValueError(
            f"Path must be relative and must not escape the root folder: {rel_path}"
        )

    full = (Path(root_folder) / clean).resolve()

    # Double-check the resolved path stays inside root_folder.
    root = Path(root_folder).resolve()
    try:
        full.relative_to(root)
    except ValueError:
        raise ValueError(f"Path escapes the allowed folder: {rel_path}")

    return full

# ── tools ─────────────────────────────────────────────────────────────────────

@mcp.tool()
def list_files(
    path: Optional[str] = None,
    pattern: Optional[str] = None,
) -> str:
    """List all files inside the root folder, recursively traversing sub-directories.

    Parameters
    ----------
    path : str, optional
        Relative sub-directory to list.  Omit to list from the root folder.
    pattern : str, optional
        Shell-style wildcard pattern (e.g. ``"*.log"``, ``"**/data*"``).
        Uses fnmatch against each file's relative path.  Omit to match all files.
    """
    target = Path(root_folder).resolve() if path is None else safe_resolve(path)

    if not target.exists():
        return f"Path not found: {path or '.'}"
    if not target.is_dir():
        return f"Path is not a directory: {path or '.'}"

    lines: list[str] = []
    root = Path(root_folder).resolve()
    count = 0

    for entry in sorted(target.rglob("*")):
        if not entry.is_file():
            continue
        rel = str(entry.relative_to(root))
        if pattern and not fnmatch.fnmatch(rel, pattern):
            continue
        size = entry.stat().st_size
        lines.append(f"{rel}  ({_human_size(size)})")
        count += 1

    if count == 0:
        return f"No files found{'' if pattern is None else f' matching \"{pattern}\"'} in: {path or '.'}"

    return "\n".join(lines)


@mcp.tool()
def read_file(
    path: str,
    encoding: Optional[str] = "utf-8",
) -> str:
    """Read the contents of a file inside the allowed folder.

    Parameters
    ----------
    path : str
        Relative path from the root folder (e.g. ``"config.json"`` or
        ``"subdir/data.txt"``).  **Required.**
    encoding : str, optional
        Text encoding to use.  Defaults to ``"utf-8"``.
    """
    if not path:
        return "Error: missing required argument 'path'"

    try:
        fp = safe_resolve(path)
    except ValueError as exc:
        return f"Error: {exc}"

    if not fp.exists():
        return f"Error: file not found: {path}"
    if fp.is_dir():
        return f"Error: path is a directory, not a file: {path}"

    try:
        return fp.read_text(encoding=encoding or "utf-8")
    except UnicodeDecodeError:
        return (
            f"Error: cannot decode file as {encoding or 'utf-8'}. "
            f"Try a different encoding (e.g. 'latin-1', 'utf-16')."
        )
    except Exception as exc:
        return f"Error reading file: {exc}"


# ── helpers ───────────────────────────────────────────────────────────────────

def _human_size(size: int) -> str:
    """Return a human-readable size string (e.g. '1.5 KB')."""
    for unit in ("B", "KB", "MB", "GB", "TB"):
        if abs(size) < 1024.0:
            return f"{size:.1f} {unit}" if unit != "B" else f"{size} B"
        size /= 1024.0
    return f"{size:.1f} PB"


# ── entry point ───────────────────────────────────────────────────────────────

def main() -> None:
    global root_folder

    parser = argparse.ArgumentParser(
        description="MCP stdio server — list & read files from a root folder."
    )
    parser.add_argument(
        "--root",
        required=True,
        help="Root folder to serve files from (required)",
    )
    # parse_known_args leaves any FastMCP/transport flags for mcp.run().
    args, _ = parser.parse_known_args()

    root_folder = os.path.abspath(args.root)
    if not os.path.isdir(root_folder):
        print(f"Not a valid directory: {root_folder}", file=sys.stderr)
        sys.exit(1)

    print(f"foldermcp — serving files from: {root_folder}", file=sys.stderr)
    mcp.run(transport="stdio")


if __name__ == "__main__":
    main()
