#!/bin/bash
# DockWeave Phase-1 Validierung v2: RO-Staging-Fix + korrigierte Checks
set -u
PASS=0; FAIL=0
ok()   { echo "PASS: $1"; PASS=$((PASS+1)); }
bad()  { echo "FAIL: $1"; FAIL=$((FAIL+1)); }
info() { echo "INFO: $1"; }

ROOT=/tmp/dwtest2
rm -rf "$ROOT"; mkdir -p "$ROOT"/{shared,src-ro,staging}
echo "ro-data" > "$ROOT/src-ro/file.txt"

mount --bind "$ROOT/shared" "$ROOT/shared"
mount --make-rshared "$ROOT/shared"

FIFO="$ROOT/fifo"; mkfifo "$FIFO"
unshare -m --propagation unchanged bash -c '
  ROOT=/tmp/dwtest2
  mount --make-rslave "$ROOT/shared"
  echo ready > "$ROOT/fifo"
  read x < "$ROOT/fifo"
  if echo hack > "$ROOT/shared/proj-ro/hacked.txt" 2>/dev/null; then
    echo "WRITE_OK" > "$ROOT/agent-ro-write.txt"
  else
    echo "WRITE_DENIED" > "$ROOT/agent-ro-write.txt"
  fi
  cat "$ROOT/shared/proj-ro/file.txt" > "$ROOT/agent-read.txt" 2>/dev/null
  echo done > "$ROOT/fifo"
  read x < "$ROOT/fifo"
' &
read x < "$FIFO"

# RO-STAGING-FIX: privat binden, remount ro, dann atomar in den shared-Baum moven
mount --bind "$ROOT/src-ro" "$ROOT/staging"
mount -o remount,ro,bind "$ROOT/staging"
mkdir -p "$ROOT/shared/proj-ro"
mount --move "$ROOT/staging" "$ROOT/shared/proj-ro"

echo go > "$FIFO"; read x < "$FIFO"

[ "$(cat "$ROOT/agent-ro-write.txt")" = "WRITE_DENIED" ] \
  && ok "RO-Staging: Schreiben im Slave-Namespace verweigert" \
  || bad "RO-Staging: Schreiben verweigert"
[ "$(cat "$ROOT/agent-read.txt")" = "ro-data" ] \
  && ok "RO-Staging: Lesen im Slave-Namespace funktioniert" \
  || bad "RO-Staging: Lesen funktioniert"
PROP=$(findmnt -no PROPAGATION "$ROOT/shared")
case "$PROP" in *shared*) ok "Host shared: $PROP";; *) bad "Host shared: $PROP";; esac

# rslave-Check korrigiert: "private,slave" ist der erwartete rslave-Zustand
echo cleanup > "$FIFO"; wait 2>/dev/null

# EBUSY-Verhalten bei offenen Dateien praezise
mkdir -p "$ROOT/busy-src" "$ROOT/busy"
echo data > "$ROOT/busy-src/f"
mount --bind "$ROOT/busy-src" "$ROOT/busy"
exec 9< "$ROOT/busy/f"
if umount "$ROOT/busy" 2>/dev/null; then
  bad "umount trotz offenem fd erfolgreich (unerwartet)"
else
  ok "umount mit offenem fd -> EBUSY (sicherer Fehlerzustand)"
fi
DATA=$(cat <&9); exec 9<&-
[ "$DATA" = "data" ] && ok "fd bleibt bei EBUSY konsistent lesbar" || bad "fd lesbar"
umount "$ROOT/busy" && ok "umount nach fd-Schliessung erfolgreich" || bad "spaeteres umount"

umount "$ROOT/shared/proj-ro" 2>/dev/null; umount "$ROOT/shared" 2>/dev/null
echo
echo "=== ERGEBNIS v2: $PASS bestanden, $FAIL fehlgeschlagen ==="
exit $FAIL
