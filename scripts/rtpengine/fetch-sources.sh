#!/bin/sh
# Downloads the rtpengine files scripts/gen_options.py and scripts/gen_ngflags.py read,
# one copy per release branch.
set -eu
dir=${1:-bin/rtpengine-src}
mkdir -p "$dir"
base=https://raw.githubusercontent.com/sipwise/rtpengine
for b in mr6.5 mr7.5 mr8.5 mr9.5 mr10.5 mr11.5 mr12.5 mr13.0 mr13.2 mr13.3 master; do
	curl -fsS -o "$dir/main-$b.c" "$base/$b/daemon/main.c"
	curl -fsS -o "$dir/rec-$b.c" "$base/$b/recording-daemon/main.c"
	curl -fsS -o "$dir/aux-$b.c" "$base/$b/lib/auxlib.c"
	echo "fetched $b"
done
# NG flags for signalling templates (mr13.2+); master moved the flag
# handlers from call_interfaces.c to call_flags.c
for b in mr13.2 mr13.3 master; do
	curl -fsS -o "$dir/ng-$b.md" "$base/$b/docs/ng_control_protocol.md"
	curl -fsS -o "$dir/callif-$b.c" "$base/$b/daemon/call_interfaces.c"
	curl -fsS -o "$dir/callflags-$b.c" "$base/$b/daemon/call_flags.c" 2>/dev/null || rm -f "$dir/callflags-$b.c"
	echo "fetched NG flags of $b"
done
