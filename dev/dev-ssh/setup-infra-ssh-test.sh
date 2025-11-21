#!/usr/bin/env bash
set -euo pipefail

# ==========================================================
# CONFIGURATION
# ==========================================================

INFRA_SERVER_URL="${INFRA_SERVER_URL:-https://172.17.0.1:9443}"
INFRA_ACCESS_KEY="${INFRA_ACCESS_KEY:-1QT6AMNsvJ.FOCH4Dpu9yEjApNvlRJdC0qR}"

IMAGE="infra-ssh-test"
CONTAINER="infra-ssh-test-1"

# The port exposed by docker on the host:
PORT=22001

echo "[*] Building container image..."
docker build -t "$IMAGE" -f  dev/dev-ssh/Dockerfile.infra-ssh-test .

# ==========================================================
# RUN CONTAINER
# ==========================================================

echo "[*] Starting container $CONTAINER..."
docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
docker run --restart=always --detach --privileged \
  --name "$CONTAINER" --publish ${PORT}:22 "$IMAGE" >/dev/null

echo "[*] Waiting for SSH to come up..."
for i in {1..30}; do
  if nc -zv 127.0.0.1 $PORT >/dev/null 2>&1; then
    echo "[*] SSH is ready."
    break
  fi
  sleep 1
done

# ==========================================================
# SSH COMMAND FUNCTION
# ==========================================================

ssh_do() {
  ssh -o StrictHostKeyChecking=no -p $PORT root@127.0.0.1 "$@"
}

# ==========================================================
# LOGIN / CREATE CONNECTOR KEY
# ==========================================================

echo "[*] Creating connector key…"

ssh_do "INFRA_SERVER=$INFRA_SERVER_URL INFRA_ACCESS_KEY=$INFRA_ACCESS_KEY infra login $INFRA_SERVER_URL --skip-tls-verify"
ssh_do "INFRA_SERVER=$INFRA_SERVER_URL INFRA_ACCESS_KEY=$INFRA_ACCESS_KEY infra keys remove test-ssh --connector --force || true"

CONNECTOR_KEY=$(ssh_do "INFRA_SERVER=$INFRA_SERVER_URL INFRA_ACCESS_KEY=$INFRA_ACCESS_KEY infra keys add --connector --name test-ssh -q")
ssh_do "INFRA_SERVER=$INFRA_SERVER_URL INFRA_ACCESS_KEY=$INFRA_ACCESS_KEY infra logout"

echo "[*] Connector key: $CONNECTOR_KEY"

# ==========================================================
# CREATE CONNECTOR CONFIG
# ==========================================================
echo "[*] Writing /etc/infra/connector.yaml..."

HOST_IP=$(docker inspect -f '{{range.NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $CONTAINER)

ssh_do "mkdir -p /etc/infra"

ssh_do "cat > /etc/infra/connector.yaml" <<EOF
kind: ssh
name: test-ssh
endpointAddr: $HOST_IP
server:
  url: $INFRA_SERVER_URL
  accessKey: $CONNECTOR_KEY
EOF

echo "[*] Updating sshd_config…"

ssh_do "bash -s" <<'EOF'
set -e
grep -q "INFRA ANSIBLE MANAGED" /etc/ssh/sshd_config || cat >> /etc/ssh/sshd_config <<CONFIG

# --- INFRA ANSIBLE MANAGED BLOCK ---
Match group infra-users
  AuthorizedKeysFile none
  PasswordAuthentication no
  AuthorizedKeysCommand /usr/bin/infra sshd auth-keys %u %f
  AuthorizedKeysCommandUser infra
# -----------------------------------
CONFIG
EOF

echo "[*] Starting infra connector..."

ssh_do "cat > /etc/init.d/infra" <<'EOF'
#!/bin/sh
### BEGIN INIT INFO
# Provides:          infra
# Required-Start:    $remote_fs $network
# Required-Stop:     $remote_fs $network
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: Infra Connector Service
### END INIT INFO

DAEMON=/usr/bin/infra
DAEMON_OPTS="connector -f /etc/infra/connector.yaml --server-skip-tls-verify"
PIDFILE=/var/run/infra.pid
LOGFILE=/var/log/infra.log

case "$1" in
  start)
    echo "Starting infra connector..."
    nohup $DAEMON $DAEMON_OPTS >> $LOGFILE 2>&1 &
    echo $! > $PIDFILE
    ;;
  stop)
    echo "Stopping infra connector..."
    [ -f $PIDFILE ] && kill "$(cat $PIDFILE)" && rm -f $PIDFILE
    ;;
  restart)
    $0 stop
    sleep 1
    $0 start
    ;;
  status)
    if [ -f $PIDFILE ]; then
        echo "infra running with PID $(cat $PIDFILE)"
    else
        echo "infra is not running"
    fi
    ;;
  *)
    echo "Usage: /etc/init.d/infra {start|stop|restart|status}"
    exit 1
    ;;
esac

exit 0
EOF

ssh_do "chmod +x /etc/init.d/infra"

ssh_do "mkdir -p /var/log && touch /var/log/infra.log"

ssh_do "service infra start"

ssh_do "service ssh restart"



echo ""
echo "=================================================="
echo "Infra SSH test container is ready!"
echo "SSH: ssh root@127.0.0.1 -p $PORT"
echo "Container: $CONTAINER"
echo "Connector key injected: $CONNECTOR_KEY"
echo "=================================================="
