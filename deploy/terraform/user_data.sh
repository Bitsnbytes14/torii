#!/bin/bash
# First-boot bootstrap: runs once, as root, via cloud-init.
# Progress: `sudo tail -f /var/log/cloud-init-output.log` on the instance.
set -euxo pipefail

# t3.micro has 1 GiB of RAM, and k3s plus Traefik, metrics-server, Postgres,
# Redis and five app pods sit right at that edge. A small swapfile turns an
# OOM-kill under a memory spike into a slowdown. k3s runs the kubelet with
# fail-swap-on=false, so swap doesn't stop it from starting.
if [ ! -f /swapfile ]; then
  fallocate -l 1G /swapfile
  chmod 600 /swapfile
  mkswap /swapfile
  swapon /swapfile
  echo '/swapfile none swap sw 0 0' >> /etc/fstab
fi

curl -sfL https://get.k3s.io | sh -

until [ -f /etc/rancher/k3s/k3s.yaml ]; do sleep 2; done

# The admin kubeconfig k3s writes is root-only. Give the ubuntu user its own
# copy so it can be fetched with scp and used by the deploy workflow's SSH
# session. It points at https://127.0.0.1:6443, which is exactly what an SSH
# tunnel exposes locally, so it works unmodified from a laptop.
install -m 600 -o ubuntu -g ubuntu /etc/rancher/k3s/k3s.yaml /home/ubuntu/kubeconfig.yaml
echo 'export KUBECONFIG=$HOME/kubeconfig.yaml' >> /home/ubuntu/.bashrc

touch /var/lib/torii-bootstrap-done
