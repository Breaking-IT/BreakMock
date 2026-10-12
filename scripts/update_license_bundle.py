#!/usr/bin/env python3
"""Bundle the canonical license files for embedding; run after updating notices."""
from pathlib import Path

root = Path(__file__).resolve().parent.parent
bundle = ((root / "LICENSE").read_text() + "\n" +
          (root / "NOTICE.txt").read_text() + "\nThird-party notices\n\n" +
          (root / "third_party/NOTICES.txt").read_text())
(root / "internal/licenseassets/notices.txt").write_text(bundle)
