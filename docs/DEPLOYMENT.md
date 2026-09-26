# Deploying to AWS

How to run Torii on AWS with the Terraform in
[deploy/terraform](../deploy/terraform) and the Kubernetes manifests in
[deploy/k8s](../deploy/k8s).

Everything runs on one t3.micro with k3s. There's no EKS (its control plane
alone is ~$73/month), no NAT Gateway, no load balancer, and no Elastic IP.

> **Cost:** this setup is near-free only inside your account's AWS free
> tier or credits. Outside those, the instance, its public IPv4 address and
> the EBS volume all bill hourly, and running 24/7 will exceed the $2
> budget. The budget **sends alerts, it doesn't
> stop anything**. When you're done, run `terraform destroy` ([below](#tearing-it-down)).

## 1. Provision the node

You need Terraform ≥ 1.7, AWS credentials (`aws configure`, or
`AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`), and an SSH key:

```sh
ssh-keygen -t ed25519 -f ~/.ssh/torii

cd deploy/terraform
cp terraform.tfvars.example terraform.tfvars   # set your IP /32, public key, email
terraform init
terraform plan -out tfplan                      # read it before applying
terraform apply tfplan
```

`terraform output` prints the public IP and ready-made commands. The IP
**changes every time the instance is stopped and started**. After a
restart, run `terraform apply -refresh-only` then `terraform output` to get the new one.

AWS emails the budget address a confirmation link. Alerts only arrive
after you've confirmed it.

If your account already has GitHub's OIDC provider (from another project),
set `create_github_oidc_provider = false`. AWS allows only one per
account.

## 2. Get a kubeconfig

k3s installs on first boot, which takes a minute or two. The k3s API
(6443) isn't open to the internet, so kubectl reaches it through an SSH
tunnel:

```sh
ssh -i ~/.ssh/torii ubuntu@$IP 'cloud-init status --wait'   # blocks until bootstrap finishes
eval "$(terraform output -raw fetch_kubeconfig_command)"    # scp → ./torii-kubeconfig.yaml (gitignored)
eval "$(terraform output -raw kubectl_tunnel_command)" &     # forwards localhost:6443
export KUBECONFIG=$PWD/torii-kubeconfig.yaml
kubectl get nodes

# The HPAs rely on k3s's bundled metrics-server. Confirm it's up:
kubectl -n kube-system get deploy metrics-server
kubectl top nodes
```

## 3. Create the Secrets (by hand, never committed)

[deploy/k8s/secrets.yaml.example](../deploy/k8s/secrets.yaml.example) shows
their shape. Create the real ones directly:

```sh
kubectl apply -f deploy/k8s/00-namespace.yaml

PG_PASSWORD="$(openssl rand -hex 16)"
kubectl -n torii create secret generic torii-secrets \
  --from-literal=POSTGRES_PASSWORD="$PG_PASSWORD" \
  --from-literal=DATABASE_URL="postgres://torii:$PG_PASSWORD@postgres:5432/torii?sslmode=disable" \
  --from-literal=JWT_SECRET="$(openssl rand -hex 32)" \
  --from-literal=CONTROLPLANE_ADMIN_TOKEN="$(openssl rand -hex 32)"

# GHCR packages are private by default. Use a classic PAT with only read:packages.
kubectl -n torii create secret docker-registry ghcr-pull \
  --docker-server=ghcr.io --docker-username=<github-user> --docker-password=<PAT>
```

Save the admin token somewhere safe. To read it back later, run
`kubectl -n torii get secret torii-secrets -o jsonpath='{.data.CONTROLPLANE_ADMIN_TOKEN}' | base64 -d`.

## 4. Apply the manifests

This needs at least one push to `main`, so CI has published the `:main`
images:

```sh
kubectl apply -f deploy/k8s/
kubectl -n torii get pods,hpa,ingress
```

The app is at `terraform output app_url`. The control plane is not exposed
publicly; reach it with a port-forward:

```sh
kubectl -n torii port-forward svc/controlplane 8082:8082
curl -s localhost:8082/tenants -H "Authorization: Bearer $ADMIN_TOKEN"
```

Re-running `kubectl apply -f deploy/k8s/` resets images to `:main`. Run
the Deploy workflow afterwards to pin a specific SHA again.

## 5. Set up the Deploy workflow

[deploy.yml](../.github/workflows/deploy.yml) runs only when you trigger it
from the Actions tab. It opens SSH to the runner's own IP for the length of
the deploy, using a narrow IAM role assumed over GitHub OIDC. Then it runs
[deploy/scripts/rollout.sh](../deploy/scripts/rollout.sh) on the node and
closes SSH again.

In the repo's settings, create an environment named `production`. Adding
required reviewers to it gates each deploy on an approval. Then add:

| Kind     | Name                    | Value                                                   |
|----------|-------------------------|---------------------------------------------------------|
| Secret   | `EC2_HOST`              | `terraform output -raw public_ip` (update after stop/start) |
| Secret   | `EC2_SSH_KEY`           | contents of `~/.ssh/torii` (the private key)            |
| Secret   | `EC2_HOST_KEY`          | `ssh -i ~/.ssh/torii ubuntu@$IP cat /etc/ssh/ssh_host_ed25519_key.pub \| cut -d' ' -f1,2` |
| Variable | `AWS_DEPLOY_ROLE_ARN`   | `terraform output -raw github_deploy_role_arn`          |
| Variable | `EC2_SECURITY_GROUP_ID` | `terraform output -raw security_group_id`               |
| Variable | `AWS_REGION`            | e.g. `ap-south-1`                                       |

A deploy checks that the images for that SHA exist in GHCR. It then points
all three Deployments at them and waits for each rollout. If a rollout
fails, the script rolls that Deployment back, and old pods keep serving
throughout because `maxUnavailable` is 0.

If a deploy is cancelled mid-run, its temporary SSH rule can be left
behind. Look in the security group for rules described
`github-actions-deploy-*`.

## Stopping vs. tearing it down

Stopping the instance (EC2 console, or
`aws ec2 stop-instances --instance-ids $(terraform output -raw instance_id)`)
stops compute charges but keeps the disk, so the EBS volume still bills.
The public IP changes on the next start.

## Tearing it down

When you're not actively using it:

```sh
cd deploy/terraform
terraform destroy
```

This deletes the instance, and with it all Postgres and Redis data (they
live on the node's disk). It also deletes the key pair, security group,
IAM role, and the budget itself. Afterwards, check the EC2 console for
anything left in the region: volumes, snapshots, and Elastic IPs outside
Terraform's control.
