# Default VPC and a default subnet: a new VPC with private subnets would need
# a NAT Gateway for outbound traffic, which alone costs more per month than
# this whole setup is budgeted for.
data "aws_vpc" "default" {
  default = true
}

data "aws_subnets" "default" {
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.default.id]
  }
  filter {
    name   = "default-for-az"
    values = ["true"]
  }
}

data "aws_ami" "ubuntu" {
  most_recent = true
  owners      = ["099720109477"] # Canonical

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"]
  }
  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

resource "aws_key_pair" "torii" {
  key_name_prefix = "torii-"
  public_key      = var.ssh_public_key
}

resource "aws_security_group" "torii" {
  name_prefix = "torii-"
  description = "Torii single-node k3s: SSH from one CIDR, HTTP/HTTPS from anywhere"
  vpc_id      = data.aws_vpc.default.id

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_vpc_security_group_ingress_rule" "ssh" {
  security_group_id = aws_security_group.torii.id
  description       = "SSH (and kubectl via SSH tunnel)"
  cidr_ipv4         = var.ssh_allowed_cidr
  ip_protocol       = "tcp"
  from_port         = 22
  to_port           = 22
}

resource "aws_vpc_security_group_ingress_rule" "http" {
  security_group_id = aws_security_group.torii.id
  description       = "HTTP to Traefik ingress"
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "tcp"
  from_port         = 80
  to_port           = 80
}

resource "aws_vpc_security_group_ingress_rule" "https" {
  security_group_id = aws_security_group.torii.id
  description       = "HTTPS to Traefik ingress"
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
}

# The k3s API (6443) is deliberately not opened: kubectl goes through an SSH
# tunnel instead, so the cluster admin surface is only reachable from
# ssh_allowed_cidr and with the SSH key.
resource "aws_vpc_security_group_egress_rule" "all" {
  security_group_id = aws_security_group.torii.id
  description       = "Outbound for package installs, k3s download, and GHCR image pulls"
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}

resource "aws_instance" "torii" {
  ami                         = data.aws_ami.ubuntu.id
  instance_type               = "t3.micro"
  subnet_id                   = sort(data.aws_subnets.default.ids)[0]
  vpc_security_group_ids      = [aws_security_group.torii.id]
  key_name                    = aws_key_pair.torii.key_name
  associate_public_ip_address = true
  user_data                   = file("${path.module}/user_data.sh")

  # t3 instances default to "unlimited" credits, which silently bills for
  # sustained CPU above baseline. "standard" throttles to baseline instead:
  # slower under load, but it can never produce a surprise charge.
  credit_specification {
    cpu_credits = "standard"
  }

  metadata_options {
    http_tokens   = "required" # IMDSv2 only
    http_endpoint = "enabled"
  }

  root_block_device {
    volume_type           = "gp3"
    volume_size           = var.root_volume_size_gb
    encrypted             = true
    delete_on_termination = true
  }

  tags = {
    Name = "torii-k3s"
  }

  lifecycle {
    # most_recent AMI lookups change whenever Canonical publishes a new image;
    # without this, a routine plan weeks later would propose destroying the
    # node (and its hostPath Postgres data) just to move to a newer AMI.
    # user_data only runs on first boot, so changing it later would force a
    # stop/start for no effect.
    ignore_changes = [ami, user_data]
  }
}

# Alerts only: AWS Budgets never stops or caps spending. It exists so that a
# forgotten running instance shows up in an inbox instead of on the bill.
resource "aws_budgets_budget" "monthly" {
  name         = "torii-monthly"
  budget_type  = "COST"
  limit_amount = "2.0"
  limit_unit   = "USD"
  time_unit    = "MONTHLY"

  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 80
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = [var.budget_alert_email]
  }

  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = [var.budget_alert_email]
  }

  # Actual-spend alerts trail real usage by hours because billing data lags;
  # a forecast alert can fire early in the month, before the money is spent.
  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100
    threshold_type             = "PERCENTAGE"
    notification_type          = "FORECASTED"
    subscriber_email_addresses = [var.budget_alert_email]
  }
}
