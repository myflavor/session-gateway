#!/bin/bash
set -e

: "${PUID:?"Error: PUID environment variable is not set."}"
: "${PGID:?"Error: PGID environment variable is not set."}"
BASH_UID="${BASH_UID:-$PUID}"

if [ "$PUID" -lt 1000 ]; then
  echo "Error: PUID must be >= 1000 (got $PUID)." >&2
  exit 1
fi
if [ "$BASH_UID" -lt 1000 ] || [ "$BASH_UID" -gt 65533 ]; then
  echo "Error: BASH_UID must be in [1000, 65533] (got $BASH_UID)." >&2
  exit 1
fi

READONLY=false
if [ "$BASH_UID" -ne "$PUID" ]; then
  READONLY=true
fi

# docker restart 保留 /etc/passwd，上一轮的用户还在，不清掉 useradd 会失败
for u in $(getent passwd | awk -F: '$5 ~ /DOCKER_MANAGED/ {print $1}'); do
  userdel "$u" 2>/dev/null || true
done

if ! getent group "$PGID" >/dev/null 2>&1; then
  groupadd -g "$PGID" session-gateway 2>/dev/null || true
fi

for uid in "$PUID" "$BASH_UID"; do
  OCCUPANT=$(getent passwd "$uid" | cut -d: -f1 || true)
  if [ -n "$OCCUPANT" ]; then
    userdel "$OCCUPANT" 2>/dev/null || true
  fi
done

useradd -c "DOCKER_MANAGED" -M -s /usr/sbin/nologin -u "$PUID" -g "$PGID" writer

BASH_USER="writer"
if $READONLY; then
  useradd -c "DOCKER_MANAGED" -M -s /bin/bash -u "$BASH_UID" -g "$PGID" reader
  BASH_USER="reader"
fi

# 不递归 chown：存档可能有上万个文件，启动会卡很久
mkdir -p /data/session
chown "$PUID:$PGID" /data/session
chmod 750 /data/session

export BASH_USER="$BASH_USER"

# 保留 setuid/setgid 能力：主进程已降权到 writer，没有它就无法再把
# session_bash 的子进程降到 reader
exec setpriv --reuid "$PUID" --regid "$PGID" --init-groups \
  --inh-caps +setuid,+setgid --ambient-caps +setuid,+setgid \
  "$@"
