# The fast lane's python kernel (backlog 41c): one long-lived process from
# the project venv, holding the notebook's python namespace. THE CELL ID IS
# THE VARIABLE — a cell's inputs arrive under their cell ids, and a cell
# publishes by assigning to a variable named after itself (its id). The
# dependency DAG stays DECLARED in the document; this process never infers
# anything (the Hex comparison recorded on backlog 41).
#
# Protocol: one JSON object per line on stdin, one per line on stdout.
#   -> {"op": "run", "scope": "3f9a1c2e", "cell": "totals", "code": "...",
#       "tables": {"orders_base": "/path/orders_base.csv"}, "preview_rows": 100}
#   <- {"ok": true, "preview": {"columns": [...], "rows": [[...]],
#       "truncated": false}, "stdout": "...", "tabular": true,
#       "pandas": true}
#   -> {"op": "drop", "scope": "3f9a1c2e", "cells": ["totals"]}  # forget outputs
#   <- {"ok": true}
# "scope" names the DOCUMENT: one kernel serves every notebook on a
# connection, and each scope is its own namespace — a variable published in
# one project is never visible from another (the Go side tags it from the
# project path; an absent scope is the empty-string namespace).
# Any parse failure is fatal on purpose: a desynced stream dies loudly and
# the Go side respawns (and resets its python ledger with it).
#
# Tables load as pandas DataFrames when the venv has pandas, else as plain
# lists of dicts (numbers stay strings in that mode — the reply says which
# mode, and the UI can tell the reader to ask the agent for pandas).

import contextlib
import csv
import io
import json
import sys
import traceback

try:
    import pandas  # noqa: F401
    HAVE_PANDAS = True
except Exception:
    HAVE_PANDAS = False

STDOUT_TAIL = 8192

namespaces = {}


def ns_for(scope):
    """The namespace of one document, created with its own ref() on first use."""
    ns = namespaces.get(scope)
    if ns is None:
        ns = {}

        def ref(name):
            # dbt idiom, honored: ref('orders_base') and the bare variable
            # orders_base are the same value. Agents (and people) reach for
            # ref() out of habit from the SQL cells; refusing it taught
            # nothing. Closed over THIS scope's namespace, never another's.
            if name not in ns:
                raise NameError(
                    f"ref({name!r}): that cell has not run in this session — list it in this cell's inputs"
                )
            return ns[name]

        ns["ref"] = ref
        namespaces[scope] = ns
    return ns


def cell_traceback(exc):
    # Only the USER'S frames: everything above the exec()'d cell is kernel
    # machinery, and showing it points the reader at our plumbing instead of
    # their code (Josh's screenshot, 2026-09-01 18:44).
    tb = exc.__traceback__
    while tb is not None and not tb.tb_frame.f_code.co_filename.startswith("<cell"):
        tb = tb.tb_next
    if tb is None:
        return "".join(traceback.format_exception_only(type(exc), exc)).strip()
    return "".join(traceback.format_exception(type(exc), exc, tb)).strip()


def out(obj):
    sys.stdout.write(json.dumps(obj, default=str) + "\n")
    sys.stdout.flush()


def load_table(path):
    if HAVE_PANDAS:
        return pandas.read_csv(path)
    with open(path, newline="") as f:
        return list(csv.DictReader(f))


def preview_of(value, limit):
    """A tabular sample of a cell's published value, or None."""
    if HAVE_PANDAS and isinstance(value, pandas.DataFrame):
        head = value.head(limit)
        return {
            "columns": [str(c) for c in head.columns],
            "rows": [[None if pandas.isna(v) else v for v in row] for row in head.itertuples(index=False)],
            "truncated": len(value) > limit,
        }
    if isinstance(value, list) and value and isinstance(value[0], dict):
        cols = list(value[0].keys())
        rows = [[r.get(c) for c in cols] for r in value[:limit]]
        return {"columns": cols, "rows": rows, "truncated": len(value) > limit}
    if isinstance(value, (str, int, float, bool)):
        return {"columns": ["value"], "rows": [[value]], "truncated": False}
    return None


def run(req):
    cell = req["cell"]
    ns = ns_for(req.get("scope") or "")
    for name, path in (req.get("tables") or {}).items():
        ns[name] = load_table(path)

    buf = io.StringIO()
    try:
        with contextlib.redirect_stdout(buf):
            exec(compile(req["code"], f"<cell {cell}>", "exec"), ns)  # noqa: S102 -- executing the user's own cell is the kernel's job
    except Exception as e:
        out({"ok": False, "error": cell_traceback(e), "stdout": buf.getvalue()[-STDOUT_TAIL:]})
        return

    value = ns.get(cell)
    preview = preview_of(value, int(req.get("preview_rows") or 100))
    out({
        "ok": True,
        "preview": preview,
        "tabular": preview is not None,
        "stdout": buf.getvalue()[-STDOUT_TAIL:],
        "pandas": HAVE_PANDAS,
    })


def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        req = json.loads(line)
        try:
            if req["op"] == "run":
                run(req)
            elif req["op"] == "peek":
                # A preview of an already-published value, no re-execution —
                # the unchanged-target path. Missing means the namespace was
                # lost (a fresh kernel); the Go side then runs for real.
                cell = req["cell"]
                ns = ns_for(req.get("scope") or "")
                if cell not in ns:
                    out({"ok": False, "error": f"{cell} is not in the namespace"})
                else:
                    p = preview_of(ns[cell], int(req.get("preview_rows") or 100))
                    out({"ok": True, "preview": p, "tabular": p is not None,
                         "stdout": "", "pandas": HAVE_PANDAS})
            elif req["op"] == "drop":
                ns = ns_for(req.get("scope") or "")
                for c in req.get("cells") or []:
                    ns.pop(c, None)
                out({"ok": True})
            else:
                out({"ok": False, "error": f"unknown op {req['op']!r}"})
        except Exception as e:  # a kernel-level fault, not the cell's
            out({"ok": False, "error": str(e)})


if __name__ == "__main__":
    main()
