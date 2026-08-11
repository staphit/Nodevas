# Terraform 1.5 is the floor. The configuration leans on lifecycle
# preconditions to refuse a plan that would cost the operator real money before
# it ever reaches Oracle's API, and on nested variable validation messages; both
# behave inconsistently in the 1.0 and 1.1 releases the provider nominally still
# supports.
#
# The provider is pinned to the 8.x line rather than left open. The OCI provider
# ships several releases a month and has renamed resource attributes across
# major versions; an unpinned provider means a deployment that planned cleanly
# in one session fails to plan in the next, which is the worst possible
# behaviour for infrastructure somebody only touches twice a year.
terraform {
  required_version = ">= 1.5.0"

  required_providers {
    oci = {
      source  = "oracle/oci"
      version = "~> 8.0"
    }
  }
}
