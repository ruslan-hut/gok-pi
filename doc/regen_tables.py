#!/usr/bin/env python3
"""Re-extract the LUNA2000B point table and alarm table from the vendor PDF.

The Go files in battery/driver/huawei were generated with this, not typed by
hand. Re-run it if Huawei issues a revision of the document, then diff the
result against the current tables.

    brew install poppler          # for pdftotext
    python3 doc/regen_tables.py "doc/LUNA2000B ESS Modbus Port Definitions.pdf"

Writes registers.json and alarms.json next to the PDF and prints the validation
results. It does not rewrite the .go files: the Go names (SOC, RatedCapacity,
ActivePowerSetpoint, ...) are a curated mapping that lives in registers.go, so
the intended workflow is to diff the JSON against the committed tables and edit
what actually changed.

Two traps this handles, both of which silently drop rows if ignored:
  - full-width CJK glyphs (U+FF08 etc.) bleed one character left into the
    previous column of pdftotext -layout output
  - a row number such as 154 wraps as "15" then "4" on consecutive lines
"""

import json
import re
import subprocess
import sys
from pathlib import Path

REG_HDR = re.compile(r"No\.\s+Signal\s+Re\s+Ty\s+Un\s+Gai\s+Addr\s+Qua\s+Scope")
ALARM_HDR = re.compile(r"ID\s+Alarm Name\s+Register Address\s+Bit")
HDR_CONT = ("ad/", "Wr", "ite", "pe", "it", "n", "ess", "ntit", "y")
NORM = str.maketrans({"（": "(", "）": ")", "：": ":", "，": ",", "【": "[", "】": "]"})
TYPES = ("U16", "U32", "U64", "I16", "I32", "I64", "Bitfield16", "Bitfield32", "STR", "MLD")


def layout_text(pdf: Path, first: int, last: int) -> str:
    return subprocess.run(
        ["pdftotext", "-layout", "-f", str(first), "-l", str(last), str(pdf), "-"],
        check=True, capture_output=True, text=True,
    ).stdout


def blocks(text, header, columns):
    """Yield (column offsets, lines) for each table row, split on blank lines."""
    for page in text.split("\f"):
        lines = page.split("\n")
        for i, line in enumerate(lines):
            if not header.search(line):
                continue

            cols, pos = [], 0
            for tok in columns:
                p = line.index(tok, pos)
                cols.append(p)
                pos = p + len(tok)
            cols.append(10**6)

            body = []
            for x in lines[i + 1:]:
                if "Issue 01" in x or "Copyright" in x or header.search(x):
                    break
                body.append(x)
            while body and (not body[0].strip()
                            or (body[0].split() and all(t in HDR_CONT for t in body[0].split()))):
                body.pop(0)

            cur = []
            for x in body:
                if not x.strip():
                    if cur:
                        yield cols, cur
                    cur = []
                else:
                    cur.append(x)
            if cur:
                yield cols, cur


def cells(line, cols, n, fix_bleed):
    out = [line[cols[i]:cols[i + 1]].strip() for i in range(n)]
    if fix_bleed:
        stray = "".join(c for c in out[0] if not c.isdigit())
        if stray:
            out[0] = "".join(c for c in out[0] if c.isdigit())
            out[1] = (stray + out[1]).strip()
    return out


def join_name(frags):
    out = ""
    for f in frags:
        if not out:
            out = f
        elif out.endswith(("-", "/")) or f.startswith("（"):
            out += f
        else:
            out += " " + f
    return out


def parse_registers(pdf):
    recs = []
    for cols, blk in blocks(layout_text(pdf, 8, 24), REG_HDR,
                            ("No.", "Signal", "Re", "Ty", "Un", "Gai", "Addr", "Qua", "Scope")):
        cs = [cells(l, cols, 9, True) for l in blk]
        no = "".join(c[0] for c in cs)
        if not no.isdigit() or int(no) > 1000:
            continue
        ad = "".join(c[6] for c in cs)
        if not ad.isdigit() or len(ad) % 5:
            continue

        rw = "".join(c[2] for c in cs)
        if rw not in ("RO", "RW", "WO"):
            m = re.search(r"\b(RO|RW|WO)\b", " ".join(blk))
            rw = m.group(1) if m else rw

        name = re.sub(r"\s*\b(RO|RW|WO)\b\s*$", "", join_name([c[1] for c in cs if c[1]]))
        recs.append({
            "no": int(no), "name": name.translate(NORM), "rw": rw,
            "type": "".join(c[3] for c in cs), "unit": "".join(c[4] for c in cs),
            "gain": "".join(c[5] for c in cs),
            "addrs": [int(ad[i:i + 5]) for i in range(0, len(ad), 5)],
            "qty": "".join(c[7] for c in cs),
            "scope": " ".join(c[8] for c in cs if c[8]).translate(NORM),
        })
    return recs


def parse_alarms(pdf):
    recs = []
    for cols, blk in blocks(layout_text(pdf, 24, 36), ALARM_HDR,
                            ("ID", "Alarm Name", "Register Address", "Bit")):
        cs = [cells(l, cols, 4, False) for l in blk]
        aid = "".join(c[0] for c in cs)
        addr = "".join(c[2] for c in cs)
        bit = "".join(c[3] for c in cs)
        if not (aid.isdigit() and addr.isdigit() and bit.isdigit()):
            continue
        recs.append({"id": int(aid), "name": join_name([c[1] for c in cs if c[1]]),
                     "addr": int(addr), "bit": int(bit)})
    return recs


def validate(regs, alarms):
    ok = True

    nums = [r["no"] for r in regs]
    print(f"registers: {len(regs)}")
    if nums != list(range(1, len(regs) + 1)):
        print(f"  FAIL numbering not sequential; missing {sorted(set(range(1, max(nums) + 1)) - set(nums))}")
        ok = False

    for r in regs:
        if r["rw"] not in ("RO", "RW", "WO") or r["type"] not in TYPES \
                or not r["gain"].isdigit() or not r["addrs"]:
            print(f"  FAIL malformed row {r}")
            ok = False

    seen = {}
    for r in regs:
        for a in r["addrs"]:
            if a in seen and seen[a] != r["no"]:
                print(f"  FAIL address {a} used by rows {seen[a]} and {r['no']}")
                ok = False
            seen[a] = r["no"]

    words = set(range(30455, 30467)) | set(range(30510, 30518)) | set(range(32133, 32165))
    print(f"alarms: {len(alarms)} across {len({a['addr'] for a in alarms})} alarm words")
    if [a["id"] for a in alarms] != sorted(a["id"] for a in alarms):
        print("  FAIL alarms not ordered by ID")
        ok = False
    for a in alarms:
        if not 0 <= a["bit"] <= 15 or a["addr"] not in words:
            print(f"  FAIL malformed alarm {a}")
            ok = False
    pairs = [(a["addr"], a["bit"]) for a in alarms]
    if len(set(pairs)) != len(pairs):
        print("  FAIL duplicate (register, bit)")
        ok = False
    ids = [a["id"] for a in alarms]
    if len(set(ids)) != len(ids):
        print("  FAIL duplicate alarm IDs")
        ok = False

    print("OK" if ok else "VALIDATION FAILED")
    return ok


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)

    pdf = Path(sys.argv[1])
    regs = parse_registers(pdf)
    alarms = parse_alarms(pdf)

    (pdf.parent / "registers.json").write_text(json.dumps(regs, ensure_ascii=False, indent=1))
    (pdf.parent / "alarms.json").write_text(json.dumps(alarms, ensure_ascii=False, indent=1))

    sys.exit(0 if validate(regs, alarms) else 1)


if __name__ == "__main__":
    main()
