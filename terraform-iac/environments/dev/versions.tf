terraform {
  required_version = ">= 1.6.0, < 2.0.0"

  required_providers {
    local = {
      source  = "hashicorp/local"
      version = "~> 2.5"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
    null = {
      source  = "hashicorp/null"
      version = "~> 3.2"
    }
  }

  # No backend block. This example runs with local state on purpose so it works
  # offline with no account and no credentials -- see "Remote state and
  # workspaces" in ../../README.md for the block a real environment carries and
  # why it belongs here rather than in the module.
}
