#!/usr/bin/env python3
"""
Lottery Number Predictor
~~~~~~~~~~~~~~~~~~~~~~~~~
Predicts 6 unique lottery numbers using weighted random sampling based on
historical winning frequency data from a CSV file.

Algorithm: Weighted Random Sampling Without Replacement
  Each number's selection probability is proportional to its historical
  frequency. After each pick, the selected number is removed and the
  remaining probabilities are renormalized. This respects the observed
  empirical distribution while acknowledging the underlying randomness
  of lottery draws.

Strategies:
  - weighted (default):   Weighted random sampling from the full pool.
  - hot:                  Picks the N most frequent numbers directly.
  - cold:                 Picks the N least frequent (gambler's fallacy — due).
  - balanced:             Mix of hot (3) + weighted random (3).
"""

from __future__ import annotations

import argparse
import csv
import random
import sys
from collections import Counter
from dataclasses import dataclass
from pathlib import Path
from typing import List, Optional, Tuple


# ---------------------------------------------------------------------------
# Data model
# ---------------------------------------------------------------------------

@dataclass
class FrequencyEntry:
    """One row from the frequency CSV."""
    number: int       # Lottery ball number (e.g. 1 … 49)
    frequency: int    # How many times it has been drawn
    probability: float = 0.0  # Computed weight (normalised frequency)


# ---------------------------------------------------------------------------
# CSV loader
# ---------------------------------------------------------------------------

def load_frequencies(path: Path, col_number: str = "No",
                     col_freq: str = "Frequency") -> List[FrequencyEntry]:
    """
    Read a frequency CSV and return a list of FrequencyEntry objects.

    Expected columns:
      No        — Ball number (e.g. 01, 02 … treated as int)
      Frequency — Integer count of historical wins

    The header row is auto-detected by matching *col_number* and
    *col_freq* (case-insensitive).  Non-numeric values are silently
    skipped.
    """
    entries: List[FrequencyEntry] = []

    with open(path, newline="") as fh:
        reader = csv.DictReader(fh)

        # Sanity check — make sure the expected columns exist
        if reader.fieldnames is None:
            print("error: empty CSV file", file=sys.stderr)
            sys.exit(1)

        lower_map = {h.lower(): h for h in reader.fieldnames}
        key_num = lower_map.get(col_number.lower())
        key_freq = lower_map.get(col_freq.lower())

        if key_num is None or key_freq is None:
            print(
                f"error: expected columns '{col_number}' and '{col_freq}', "
                f"got {reader.fieldnames}",
                file=sys.stderr,
            )
            sys.exit(1)

        for row in reader:
            try:
                num = int(row[key_num])
                freq = int(row[key_freq])
            except (ValueError, TypeError):
                continue  # Skip unparseable rows
            entries.append(FrequencyEntry(number=num, frequency=freq))

    if not entries:
        print("error: no valid frequency data found", file=sys.stderr)
        sys.exit(1)

    # Sort by number so the output is deterministic-order
    entries.sort(key=lambda e: e.number)

    # Normalise → probabilities
    total_freq = sum(e.frequency for e in entries)
    for e in entries:
        e.probability = e.frequency / total_freq

    return entries


# ---------------------------------------------------------------------------
# Prediction strategies
# ---------------------------------------------------------------------------

def predict_weighted(entries: List[FrequencyEntry], count: int = 6) -> List[int]:
    """
    Weighted random sampling **without replacement**.

    Algorithm (iterative):
      1. Compute selection probabilities proportional to historical
         frequency for the *remaining* pool.
      2. Pick one number via those probabilities.
      3. Remove it and repeat until *count* numbers are selected.

    This is the statistically principled method: it respects the observed
    distribution while allowing for variety across predictions.
    """
    pool = [e for e in entries]  # copy
    selected: List[int] = []

    for _ in range(count):
        if not pool:
            break
        total = sum(e.frequency for e in pool)
        weights = [e.frequency / total for e in pool]
        choice = random.choices(pool, weights=weights, k=1)[0]
        selected.append(choice.number)
        pool = [e for e in pool if e.number != choice.number]

    return sorted(selected)


def predict_hot(entries: List[FrequencyEntry], count: int = 6) -> List[int]:
    """Pick the *count* most frequently drawn numbers (pure hot-numbers)."""
    sorted_entries = sorted(entries, key=lambda e: e.frequency, reverse=True)
    return sorted(e.number for e in sorted_entries[:count])


def predict_cold(entries: List[FrequencyEntry], count: int = 6) -> List[int]:
    """Pick the *count* least frequently drawn numbers (cold / due)."""
    sorted_entries = sorted(entries, key=lambda e: e.frequency)
    return sorted(e.number for e in sorted_entries[:count])


def predict_balanced(entries: List[FrequencyEntry], count: int = 6) -> List[int]:
    """
    Balanced strategy:  ½ hot + ½ weighted random, preserving uniqueness.

    Hot picks the historically strongest numbers; weighted adds statistical
    variety.  Duplicates between the two groups are deduplicated, then
    any missing slots are filled with additional weighted picks.
    """
    hot_count = count // 2
    hot_picks = set(predict_hot(entries, hot_count))
    weight_picks = set(predict_weighted(entries, count))

    combined = list(hot_picks | weight_picks)

    # Fill remaining slots if dedup shrank the set
    pool = [e for e in entries if e.number not in combined]
    while len(combined) < count and pool:
        total = sum(e.frequency for e in pool)
        weights = [e.frequency / total for e in pool]
        choice = random.choices(pool, weights=weights, k=1)[0]
        combined.append(choice.number)
        pool = [e for e in pool if e.number != choice.number]

    return sorted(combined[:count])


# ---------------------------------------------------------------------------
# Statistics helpers
# ---------------------------------------------------------------------------

def show_stats(entries: List[FrequencyEntry]) -> None:
    """Print summary statistics about the frequency distribution."""
    freqs = [e.frequency for e in entries]
    n = len(freqs)
    mean = sum(freqs) / n
    sorted_f = sorted(freqs)

    if n % 2 == 0:
        median = (sorted_f[n // 2 - 1] + sorted_f[n // 2]) / 2
    else:
        median = sorted_f[n // 2]

    variance = sum((f - mean) ** 2 for f in freqs) / n
    std_dev = variance ** 0.5

    print(f"  Pool size:         {n} numbers")
    print(f"  Mean frequency:    {mean:.1f}")
    print(f"  Median frequency:  {median:.0f}")
    print(f"  Std deviation:     {std_dev:.1f}")
    print(f"  Min frequency:     {min(freqs)} (#{entries[freqs.index(min(freqs))].number:02d})")
    print(f"  Max frequency:     {max(freqs)} (#{entries[freqs.index(max(freqs))].number:02d})")
    print()

    # Top / bottom 6
    by_freq = sorted(entries, key=lambda e: e.frequency, reverse=True)
    top6_str = ", ".join(f"#{e.number:02d} ({e.frequency})" for e in by_freq[:6])
    bot6_str = ", ".join(f"#{e.number:02d} ({e.frequency})" for e in by_freq[-6:])
    print(f"  Hottest 6:  {top6_str}")
    print(f"  Coldest 6:  {bot6_str}")


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def parse_args(argv: Optional[List[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Predict lottery numbers from historical frequency data.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "Examples:\n"
            "  %(prog)s                     one weighted prediction\n"
            "  %(prog)s -n 5                five weighted predictions\n"
            "  %(prog)s -s hot              single hot-numbers prediction\n"
            "  %(prog)s -s balanced -n 3    three balanced predictions\n"
            "  %(prog)s -v                   show frequency stats only\n"
        ),
    )
    parser.add_argument(
        "csv", nargs="?",
        default="frequency.csv",
        help="Path to frequency CSV (default: frequency.csv)",
    )
    parser.add_argument(
        "-n", "--num-sets", type=int, default=1,
        help="Number of prediction sets to generate (default: 1)",
    )
    parser.add_argument(
        "-s", "--strategy",
        choices=["weighted", "hot", "cold", "balanced"],
        default="weighted",
        help="Prediction strategy (default: weighted)",
    )
    parser.add_argument(
        "-v", "--verbose", action="store_true",
        help="Show frequency statistics and exit",
    )
    parser.add_argument(
        "--seed", type=int, default=None,
        help="Random seed for reproducible results",
    )
    return parser.parse_args(argv)


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> None:
    args = parse_args()
    csv_path = Path(args.csv)

    if not csv_path.exists():
        print(f"error: file not found — {csv_path}", file=sys.stderr)
        sys.exit(1)

    entries = load_frequencies(csv_path)

    # ---- Show statistics when -v is passed --------------------------------
    if args.verbose:
        show_stats(entries)
        return

    # ---- Seed for reproducibility ------------------------------------------
    if args.seed is not None:
        random.seed(args.seed)

    # ---- Strategy map ------------------------------------------------------
    strategies = {
        "weighted": predict_weighted,
        "hot":      predict_hot,
        "cold":     predict_cold,
        "balanced": predict_balanced,
    }
    predict_fn = strategies[args.strategy]

    # ---- Generate predictions ----------------------------------------------
    print(f"── Lottery Prediction ──────────────────────────────")
    print(f"  Strategy:  {args.strategy}")
    print(f"  Data file: {csv_path.name}")
    print()

    for i in range(1, args.num_sets + 1):
        nums = predict_fn(entries, count=6)
        # Format with leading zeros
        nice = "  ".join(f"{n:02d}" for n in nums)
        print(f"  Set {i:2d}:  {nice}")

    print()
    print(f"───────────────────────────────────────────────────")


if __name__ == "__main__":
    main()
