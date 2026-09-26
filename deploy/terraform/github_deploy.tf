# Lets the Deploy workflow reach SSH without leaving port 22 open.
#
# SSH is restricted to ssh_allowed_cidr, but GitHub-hosted runners come from
# a large, changing pool of addresses. Instead of widening the rule, the
# workflow assumes this role via GitHub OIDC (no long-lived AWS keys stored
# in GitHub), opens 22 to its own /32 for the duration of the deploy, and
# revokes it afterwards. The role can do nothing else.

# GitHub's OIDC issuer can only be registered once per AWS account. If the
# account already has it (from another project), set this to false and the
# existing one is looked up instead.
resource "aws_iam_openid_connect_provider" "github" {
  count          = var.create_github_oidc_provider ? 1 : 0
  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]
}

data "aws_iam_openid_connect_provider" "github" {
  count = var.create_github_oidc_provider ? 0 : 1
  url   = "https://token.actions.githubusercontent.com"
}

locals {
  github_oidc_provider_arn = (
    var.create_github_oidc_provider
    ? aws_iam_openid_connect_provider.github[0].arn
    : data.aws_iam_openid_connect_provider.github[0].arn
  )
}

# Policies are plain jsonencode() rather than aws_iam_policy_document data
# sources so `terraform test` (with a mocked provider) can assert on them.
locals {
  github_deploy_trust_policy = {
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRoleWithWebIdentity"
      Principal = { Federated = local.github_oidc_provider_arn }
      Condition = {
        StringEquals = {
          "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
          # Only jobs in this repo running in the "production" environment
          # can assume the role: not forks, not jobs without that environment.
          "token.actions.githubusercontent.com:sub" = "repo:${var.github_repository}:environment:production"
        }
      }
    }]
  }

  github_deploy_policy = {
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "ec2:AuthorizeSecurityGroupIngress",
        "ec2:RevokeSecurityGroupIngress",
      ]
      Resource = aws_security_group.torii.arn
    }]
  }
}

resource "aws_iam_role" "github_deploy" {
  name_prefix          = "torii-github-deploy-"
  assume_role_policy   = jsonencode(local.github_deploy_trust_policy)
  max_session_duration = 3600
}

resource "aws_iam_role_policy" "github_deploy" {
  name   = "open-ssh-for-deploy"
  role   = aws_iam_role.github_deploy.id
  policy = jsonencode(local.github_deploy_policy)
}
