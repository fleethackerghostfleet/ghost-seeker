#!/usr/bin/env sh
# Usage: degrade.sh <profile>   profiles: satcom | trickle | down | up | starlink
# Talks to the Toxiproxy admin API (localhost:8474) to shape the ship->shore link.
API=${TOXIPROXY:-http://localhost:8474}
P=ship_to_shore
reset() { curl -s -X POST "$API/reset" >/dev/null; }
bw()    { curl -s -X POST "$API/proxies/$P/toxics" -d "{\"name\":\"bw\",\"type\":\"bandwidth\",\"stream\":\"upstream\",\"attributes\":{\"rate\":$1}}" >/dev/null; }
lat()   { curl -s -X POST "$API/proxies/$P/toxics" -d "{\"name\":\"lat\",\"type\":\"latency\",\"attributes\":{\"latency\":$1,\"jitter\":$2}}" >/dev/null; }
case "$1" in
  # Operator-UI presets (bits/s -> toxiproxy KB/s). Same ladder as the
  # in-process shaper in cmd/ghost-seeker -shore and cmd/sim -bps.
  9k6)      reset; bw 1;    lat 1500 500 ;;   # ~9.6 kbps (toxiproxy's floor is 1 KB/s)
  64k)      reset; bw 8;    lat 600 200 ;;
  256k)     reset; bw 32;   lat 300 100 ;;
  2m)       reset; bw 250;  lat 60 20 ;;
  satcom)   reset; bw 8;    lat 600 200 ;;   # 64 kbps, 600 ms RTT
  trickle)  reset; bw 1;    lat 1500 500 ;;  # ~8 kbps
  starlink) reset; bw 2500; lat 40 20 ;;     # 20 Mbps
  down)     curl -s -X POST "$API/proxies/$P" -d '{"enabled":false}' >/dev/null ;;
  up)       curl -s -X POST "$API/proxies/$P" -d '{"enabled":true}' >/dev/null ;;
  *) echo "usage: $0 9k6|64k|256k|2m|satcom|trickle|starlink|down|up"; exit 2 ;;
esac
echo "link: $1"
