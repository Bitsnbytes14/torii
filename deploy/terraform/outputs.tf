output "instance_id" {
  description = "EC2 instance ID (for `aws ec2 stop-instances` / `start-instances`)."
  value       = aws_instance.torii.id
}

output "public_ip" {
  description = "Public IP of the node. There is no Elastic IP, so this CHANGES on every stop/start: run `terraform apply -refresh-only` afterwards and update the EC2_HOST GitHub secret."
  value       = aws_instance.torii.public_ip
}

output "app_url" {
  description = "Demo frontend and /api via Traefik ingress. Changes with public_ip."
  value       = "http://${aws_instance.torii.public_ip}/"
}

output "ssh_command" {
  description = "SSH into the node."
  value       = "ssh -i ~/.ssh/torii ubuntu@${aws_instance.torii.public_ip}"
}

output "fetch_kubeconfig_command" {
  description = "Copy the kubeconfig k3s wrote on first boot. It targets 127.0.0.1:6443, so use it with the SSH tunnel below."
  value       = "scp -i ~/.ssh/torii ubuntu@${aws_instance.torii.public_ip}:kubeconfig.yaml ./torii-kubeconfig.yaml"
}

output "kubectl_tunnel_command" {
  description = "Forward the k3s API over SSH (6443 is not open in the security group). Leave running while using kubectl."
  value       = "ssh -i ~/.ssh/torii -N -L 6443:127.0.0.1:6443 ubuntu@${aws_instance.torii.public_ip}"
}

output "github_deploy_role_arn" {
  description = "Set as the AWS_DEPLOY_ROLE_ARN GitHub Actions variable."
  value       = aws_iam_role.github_deploy.arn
}

output "security_group_id" {
  description = "Set as the EC2_SECURITY_GROUP_ID GitHub Actions variable; the deploy workflow opens SSH on it for its own IP only while deploying."
  value       = aws_security_group.torii.id
}
