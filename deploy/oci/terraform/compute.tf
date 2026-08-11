# The image is looked up rather than pinned to an OCID. Image OCIDs in OCI are
# per-region: the same Ubuntu build has a different identifier in Frankfurt than
# it does in Tokyo, so a hardcoded one turns changing the region variable into a
# confusing failure about an image that does not exist. Filtering by shape also
# does the architecture selection for free — the A1 shape only matches aarch64
# builds and the micro shape only matches x86_64 ones — so switching shapes does
# not require thinking about which image goes with it.
#
# Ubuntu 24.04 rather than Oracle Linux 9. Both are Always Free eligible and
# both carry the Oracle Cloud Agent this configuration relies on for the volume
# attachment. Ubuntu wins on the thing that actually matters here, which is that
# Caddy is packaged for it directly by its authors, and the rest of the
# deployment is one static Go binary that does not care either way.
resource "terraform_data" "instance_architecture" {
  # Shape families are not interchangeable in-place (for example E2 -> E5),
  # even when both are x86. Track the exact shape so Terraform performs the
  # safe create-before-destroy replacement for every family switch.
  input = var.instance_shape
}

data "oci_core_images" "ubuntu" {
  compartment_id           = var.compartment_ocid
  operating_system         = "Canonical Ubuntu"
  operating_system_version = "24.04"
  shape                    = var.instance_shape
  state                    = "AVAILABLE"
  sort_by                  = "TIMECREATED"
  sort_order               = "DESC"
}

# Compute is the one resource here where getting it wrong costs money every
# hour. A1 and E2 are Always Free; E5 Flex is deliberately available only as a
# small, acknowledged Free Trial fallback for A1 capacity failures.
resource "oci_core_instance" "app" {
  compartment_id      = var.compartment_ocid
  availability_domain = local.availability_domain
  display_name        = var.project_name
  shape               = var.instance_shape

  # fault_domain is left to Oracle. Pinning one narrows the pool of hardware the
  # launch can be satisfied from, and free A1 capacity is scarce enough already.

  # Only emitted for the flexible shape. The fixed micro shape rejects a
  # shape_config outright rather than ignoring it, so this cannot be a block
  # with conditional values inside.
  dynamic "shape_config" {
    for_each = local.is_flex_shape ? [1] : []

    content {
      ocpus         = var.instance_ocpus
      memory_in_gbs = var.instance_memory_in_gbs
    }
  }

  source_details {
    source_type             = "image"
    source_id               = data.oci_core_images.ubuntu.images[0].id
    boot_volume_size_in_gbs = var.boot_volume_size_in_gbs
  }

  create_vnic_details {
    subnet_id    = oci_core_subnet.public.id
    display_name = "${var.project_name}-vnic"

    # No ephemeral public address. The reserved one in network.tf is attached to
    # this VNIC's private IP immediately after launch, and a private IP that
    # already has an ephemeral public address will refuse it.
    assign_public_ip = false
  }

  # OCI caps both user_data and total metadata. Compress this small bootstrap;
  # large service files and scripts are uploaded to Object Storage first.
  metadata = {
    ssh_authorized_keys = trimspace(var.ssh_public_key)
    user_data = base64gzip(templatefile("${path.module}/${var.cloud_init_file}", {
      nodevas_bootstrap_script = indent(6, file("${path.module}/../files/nodevas-bootstrap.sh"))
      nodevas_data_device      = local.data_device_path
      nodevas_backup_bucket    = local.bucket_name
      nodevas_backup_namespace = data.oci_objectstorage_namespace.this.namespace
    }))
  }

  depends_on = [oci_objectstorage_object.bootstrap]

  # The Oracle Cloud Agent's block volume plugin is what performs the iSCSI
  # login for the data volume and creates the consistent device path, so that
  # neither cloud-init nor the operator has to run iscsiadm with values only
  # Terraform knows. It is enabled by default on these images, but stated here
  # because the volume attachment silently never appears if it is off.
  agent_config {
    is_management_disabled = false
    is_monitoring_disabled = false

    plugins_config {
      name          = "Block Volume Management"
      desired_state = "ENABLED"
    }
  }

  lifecycle {
    # A shape update within one CPU architecture can happen in place, but an
    # AMD-to-Ampere switch also needs a different boot image. Force that case
    # to create a replacement, and create it first so an A1 capacity failure
    # leaves the working E2 instance untouched.
    replace_triggered_by  = [terraform_data.instance_architecture]
    create_before_destroy = true

    # A precondition rather than a variable validation, because the rule spans
    # two variables and a validation block can only see its own. This is the
    # guard that keeps a 4 OCPU / 24 GB configuration copied from any tutorial
    # written before June 2026 from quietly producing a monthly bill.
    precondition {
      condition     = var.acknowledge_paid_compute || var.instance_shape == "VM.Standard.E2.1.Micro" || (local.is_ampere_shape && var.instance_ocpus <= 2 && var.instance_memory_in_gbs <= 12)
      error_message = "E5 Flex is paid and consumes Free Trial credits; set acknowledge_paid_compute = true. A1 is Always Free only up to 2 OCPUs and 12 GB."
    }

    precondition {
      condition     = var.boot_volume_size_in_gbs >= 47
      error_message = "Oracle's minimum boot volume is 47 GB regardless of shape."
    }

    precondition {
      condition     = var.create_instance_principal_iam
      error_message = "Automatic bootstrap downloads files from the private backup bucket and requires create_instance_principal_iam = true."
    }

    # The image lookup returns the newest build, which changes whenever Canonical
    # publishes one. Without this, an apply months later would destroy and
    # recreate a running instance purely because a newer image exists. Rebuilding
    # is intended to be a deliberate act — `terraform taint` — not a side effect
    # of an unrelated change.
    ignore_changes = [source_details[0].source_id]
  }
}
