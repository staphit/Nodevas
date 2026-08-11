# APIKey is suitable for unattended maintenance. SecurityToken uses an OCI CLI
# browser session and is safer for an interactive one-off deployment because it
# expires after an hour. Null API-key fields are ignored in SecurityToken mode.
provider "oci" {
  auth                = var.auth_method
  config_file_profile = var.auth_method == "SecurityToken" ? var.config_file_profile : null
  tenancy_ocid        = var.auth_method == "APIKey" ? var.tenancy_ocid : null
  user_ocid           = var.auth_method == "APIKey" ? var.user_ocid : null
  fingerprint         = var.auth_method == "APIKey" ? var.api_key_fingerprint : null
  private_key_path    = var.auth_method == "APIKey" ? var.api_private_key_path : null
  region              = var.region
}

# Always Free compute has to live in the tenancy's home region, and the
# availability domains differ from region to region, so both are looked up
# rather than assumed.
data "oci_identity_availability_domains" "this" {
  compartment_id = var.tenancy_ocid
}

# The Object Storage namespace is a tenancy-wide string that Oracle assigns and
# the operator cannot choose. Looking it up removes one more value they would
# otherwise have to hunt for in the console.
data "oci_objectstorage_namespace" "this" {
  compartment_id = var.tenancy_ocid
}
