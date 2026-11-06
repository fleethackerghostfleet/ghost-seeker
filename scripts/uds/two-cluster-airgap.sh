#!/usr/bin/env bash
set -euo pipefail
CIDR=172.19.0.0/16
GATEWAY=172.19.0.1
TAG=ghost-seeker-uds-airgap
FWD=GS-UDS-AG-FWD
HOST=GS-UDS-AG-HOST
ipt() { sudo -n iptables -w 5 "$@"; }
nodeipt() {
  local node=$1 pid
  shift
  pid=$(docker inspect -f '{{.State.Pid}}' "$node")
  sudo -n nsenter -t "$pid" -n iptables -w 5 "$@"
}
ensure() { local chain=$1; shift; ipt -C "$chain" "$@" 2>/dev/null || ipt -A "$chain" "$@"; }
remove() { local chain=$1; shift; while ipt -C "$chain" "$@" 2>/dev/null; do ipt -D "$chain" "$@"; done; }

fwd_jump=(-s "$CIDR" -m comment --comment "$TAG:forward" -j "$FWD")
host_jump=(-s "$CIDR" -m comment --comment "$TAG:host" -j "$HOST")
intra=(-d "$CIDR" -m comment --comment "$TAG:intra" -j RETURN)
link=(-d "$GATEWAY/32" -p tcp --dport 443 -m comment --comment "$TAG:shaper" -j RETURN)
reply=(-m conntrack --ctdir REPLY -m comment --comment "$TAG:reply" -j RETURN)
drop=(-m comment --comment "$TAG:deny" -j DROP)
dns=(-d 127.0.0.11/32 -m comment --comment "$TAG:docker-dns" -j DROP)
mapfile -t nodes < <(docker ps --filter network=k3d-uds-ship --format '{{.Names}}')

case "${1:-status}" in
  on)
    trap '"$0" off' ERR
    [[ "$(docker network inspect k3d-uds-ship --format '{{(index .IPAM.Config 0).Subnet}}')" == "$CIDR" ]] || {
      echo 'Unexpected ship network; refusing to change firewall' >&2; exit 1;
    }
    for chain in "$FWD" "$HOST"; do ipt -S "$chain" >/dev/null 2>&1 || ipt -N "$chain"; done
    ensure "$FWD" "${intra[@]}"
    ensure "$FWD" "${drop[@]}"
    ensure "$HOST" "${link[@]}"
    ensure "$HOST" "${reply[@]}"
    ensure "$HOST" "${drop[@]}"
    for node in "${nodes[@]}"; do
      nodeipt "$node" -C OUTPUT "${dns[@]}" 2>/dev/null || nodeipt "$node" -I OUTPUT 1 "${dns[@]}"
    done
    ipt -C DOCKER-USER "${fwd_jump[@]}" 2>/dev/null || ipt -I DOCKER-USER 1 "${fwd_jump[@]}"
    ipt -C INPUT "${host_jump[@]}" 2>/dev/null || ipt -I INPUT 1 "${host_jump[@]}"
    echo 'ON: ship forwarding restricted to its Docker subnet; host gateway restricted to TCP/443 and host-initiated replies'
    ;;
  off)
    for node in "${nodes[@]}"; do
      while nodeipt "$node" -C OUTPUT "${dns[@]}" 2>/dev/null; do
        nodeipt "$node" -D OUTPUT "${dns[@]}"
      done
    done
    remove DOCKER-USER "${fwd_jump[@]}"
    remove INPUT "${host_jump[@]}"
    if ipt -S "$FWD" >/dev/null 2>&1; then
      remove "$FWD" "${intra[@]}"
      remove "$FWD" "${drop[@]}"
      ipt -X "$FWD"
    fi
    if ipt -S "$HOST" >/dev/null 2>&1; then
      remove "$HOST" "${link[@]}"
      remove "$HOST" "${reply[@]}"
      remove "$HOST" "${drop[@]}"
      ipt -X "$HOST"
    fi
    echo 'OFF: only tagged harness rules and empty harness chains removed'
    ;;
  status)
    if ipt -C DOCKER-USER "${fwd_jump[@]}" 2>/dev/null; then
      echo ON
      ipt -S "$FWD"
      ipt -S "$HOST"
    else
      echo OFF
    fi
    for node in "${nodes[@]}"; do
      if nodeipt "$node" -C OUTPUT "${dns[@]}" 2>/dev/null; then
        echo "$node Docker DNS: BLOCKED"
      else
        echo "$node Docker DNS: UNBLOCKED"
      fi
    done
    ;;
  *) echo 'Usage: two-cluster-airgap.sh on|off|status' >&2; exit 1;;
esac
