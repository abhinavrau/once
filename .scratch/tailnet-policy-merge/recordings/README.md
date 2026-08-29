# Diff screen recordings (ticket 04)

VHS recordings of the prototype's Merged Policy diff at 24, 46 and 60 rows
(80 cols), driving the Large GitOps fixture (200+ lines). Each gif walks:
fixture pick, skip tagOwners, fill the credential form, review the diff,
scroll. The `.png` beside each gif is its final frame.

Re-record from the repo root (needs `vhs` and `ttyd` on PATH):

    vhs .scratch/tailnet-policy-merge/recordings/diff-24.tape

`PROTO_SIZE=80x24` pins the prototype's layout to the size under test, so
the pixel geometry of the VHS terminal does not have to hit 80x24 exactly.
