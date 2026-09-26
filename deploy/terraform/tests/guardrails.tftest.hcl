# Plan-time checks for the cost and security guardrails, run with
# `terraform test`. The AWS provider is mocked, so these need no credentials
# and create nothing; they assert on what a real plan would request.

mock_provider "aws" {
  override_data {
    target = data.aws_subnets.default
    values = {
      ids = ["subnet-bbbb", "subnet-aaaa"]
    }
  }
  override_data {
    target = data.aws_ami.ubuntu
    values = {
      id = "ami-0123456789abcdef0"
    }
  }
}

variables {
  ssh_allowed_cidr   = "203.0.113.7/32"
  ssh_public_key     = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKeyOnly test@example"
  budget_alert_email = "alerts@example.com"
}

run "cost_guardrails" {
  command = plan

  assert {
    condition     = aws_instance.torii.instance_type == "t3.micro"
    error_message = "Instance must be t3.micro."
  }

  assert {
    condition     = aws_instance.torii.credit_specification[0].cpu_credits == "standard"
    error_message = "CPU credits must be \"standard\"; \"unlimited\" can bill for sustained CPU."
  }

  assert {
    condition     = aws_instance.torii.root_block_device[0].volume_size <= 30
    error_message = "Root volume must stay within the 30 GB EBS free tier."
  }

  assert {
    condition     = aws_budgets_budget.monthly.limit_amount == "2.0" && aws_budgets_budget.monthly.limit_unit == "USD"
    error_message = "Budget must be $2/month."
  }

  assert {
    condition = toset([
      for n in aws_budgets_budget.monthly.notification : "${n.notification_type}:${n.threshold}"
    ]) == toset(["ACTUAL:80", "ACTUAL:100", "FORECASTED:100"])
    error_message = "Budget must alert at 80% and 100% actual, and 100% forecasted."
  }

  assert {
    condition     = aws_instance.torii.subnet_id == "subnet-aaaa"
    error_message = "Subnet choice must be deterministic (sorted), not whatever order the API returns."
  }
}

run "security_guardrails" {
  command = plan

  assert {
    condition     = aws_vpc_security_group_ingress_rule.ssh.cidr_ipv4 == "203.0.113.7/32"
    error_message = "SSH must only be open to ssh_allowed_cidr."
  }

  assert {
    condition     = aws_instance.torii.metadata_options[0].http_tokens == "required"
    error_message = "IMDSv2 must be required."
  }

  assert {
    condition     = aws_instance.torii.root_block_device[0].encrypted
    error_message = "Root volume must be encrypted."
  }
}

run "rejects_ssh_open_to_world" {
  command = plan

  variables {
    ssh_allowed_cidr = "0.0.0.0/0"
  }

  expect_failures = [var.ssh_allowed_cidr]
}

run "rejects_oversized_volume" {
  command = plan

  variables {
    root_volume_size_gb = 100
  }

  expect_failures = [var.root_volume_size_gb]
}

run "deploy_role_is_narrow" {
  command = plan

  assert {
    condition = (
      local.github_deploy_trust_policy.Statement[0].Condition.StringEquals["token.actions.githubusercontent.com:sub"]
      == "repo:Bitsnbytes14/torii:environment:production"
    )
    error_message = "Deploy role must only trust this repo's production environment."
  }

  assert {
    condition = toset(local.github_deploy_policy.Statement[0].Action) == toset([
      "ec2:AuthorizeSecurityGroupIngress",
      "ec2:RevokeSecurityGroupIngress",
    ]) && length(local.github_deploy_policy.Statement) == 1
    error_message = "Deploy role may only open/close security group ingress."
  }
}
