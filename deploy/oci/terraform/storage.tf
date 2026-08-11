# ---------------------------------------------------------------------------
# Data volume
# ---------------------------------------------------------------------------

# The workspace lives on its own volume rather than on the boot volume, because
# a boot volume is destroyed with its instance. That matters more than usual
# here: Nodevas keeps the user's actual work — node bodies as Markdown, the
# graph as YAML, the SQLite database under .vised — as ordinary files on disk.
# Losing the boot volume with the workspace on it loses the product. With the
# workspace here, rebuilding the instance is `terraform taint` plus a mount.
#
# Always Free gives 200 GB of block storage across every volume in the tenancy,
# boot volumes included. The default arithmetic is 50 GB of boot plus 50 GB
# here, which is 100 and leaves the same amount again unused — enough to grow
# this volume to 150 GB later, or to keep a second free instance alongside it.
# Volumes can be expanded online and cannot be shrunk, so starting small is the
# reversible direction.
resource "oci_core_volume" "data" {
  compartment_id      = var.compartment_ocid
  availability_domain = local.availability_domain
  display_name        = "${var.project_name}-data"
  size_in_gbs         = var.block_volume_size_in_gbs
  vpus_per_gb         = var.block_volume_vpus_per_gb

  # No backup policy is assigned. Oracle's managed Bronze, Silver and Gold
  # policies retain far more than the five volume backups Always Free includes,
  # so attaching one turns into a bill within a couple of months. Backups here
  # go to Object Storage instead, where the retention is under our control.

  lifecycle {
    # This is the volume holding the user's work. Making `terraform destroy`
    # fail loudly rather than take it away is the correct default; delete these
    # four lines when you genuinely mean to tear the deployment down.
    prevent_destroy = true

    precondition {
      condition     = var.boot_volume_size_in_gbs + var.block_volume_size_in_gbs <= 200
      error_message = "Boot and block volumes share one 200 GB Always Free allowance, and these add up to more than that. The excess is billed per gigabyte-month."
    }

    precondition {
      condition     = var.block_volume_size_in_gbs >= 50
      error_message = "Oracle's minimum block volume size is 50 GB."
    }
  }
}

# iSCSI rather than paravirtualized attachment, with the Oracle Cloud Agent
# doing the login. The agent creates the consistent device path named in
# locals.tf, so cloud-init waits for one fixed path instead of running iscsiadm
# with an IQN and portal address that only exist after the attachment is made —
# values Terraform cannot template into user data, because user data is written
# before the instance the attachment depends on. The raw iSCSI details are
# exported in the outputs anyway, for the case where the agent is unavailable
# and somebody has to log in by hand.
resource "oci_core_volume_attachment" "data" {
  attachment_type = "iscsi"
  instance_id     = oci_core_instance.app.id
  volume_id       = oci_core_volume.data.id
  device          = local.data_device_path

  is_agent_auto_iscsi_login_enabled = true

  # CHAP would add a second secret to manage for traffic that never leaves
  # Oracle's virtual network between two endpoints we own.
  use_chap = false
}

# ---------------------------------------------------------------------------
# Backup bucket
# ---------------------------------------------------------------------------

# Object Storage is Always Free up to 20 GB across the Standard, Infrequent
# Access and Archive tiers combined, plus 50,000 API requests a month. Three
# database snapshots and log archives a day are only hundreds of requests a
# month with retries, so the request budget is not a constraint; the 20 GB is,
# which is what the lifecycle rules below exist to hold.
resource "oci_objectstorage_bucket" "backups" {
  compartment_id = var.compartment_ocid
  namespace      = data.oci_objectstorage_namespace.this.namespace
  name           = local.bucket_name
  storage_tier   = "Standard"

  # Private. A bucket holding database snapshots is a bucket holding password
  # hashes, session material and the entire audit trail; NoPublicAccess is the
  # only setting that makes sense and it is stated rather than inherited so that
  # nobody has to go and check what the default was.
  access_type = "NoPublicAccess"

  # Versioning off. With versioning on, a lifecycle DELETE only writes a delete
  # marker and every superseded copy keeps occupying part of the 20 GB
  # allowance, so retention silently stops working and the first sign of it is a
  # bill. Protection against a bad backup overwriting a good one comes from
  # timestamped object names and from withholding the overwrite permission in
  # iam.tf, not from versioning.
  versioning = "Disabled"

  object_events_enabled = false
}

# Keep cloud-init below OCI's metadata limit. Terraform uploads the immutable
# bootstrap inputs before launching the VM; the instance principal downloads
# them from this private bucket on first boot.
locals {
  bootstrap_files = toset([
    "Caddyfile",
    "fail2ban-nodevas.filter",
    "fail2ban-nodevas.jail",
    "nodevas-backup.service",
    "nodevas-backup.sh",
    "nodevas-backup.timer",
    "nodevas-deploy.sh",
    "nodevas-logs",
    "nodevas-restore.sh",
    "nodevas.env.example",
    "nodevas.service",
  ])
}

resource "oci_objectstorage_object" "bootstrap" {
  for_each = local.bootstrap_files

  bucket       = oci_objectstorage_bucket.backups.name
  namespace    = data.oci_objectstorage_namespace.this.namespace
  object       = "bootstrap/${each.value}"
  source       = abspath("${path.module}/../files/${each.value}")
  content_type = "text/plain; charset=utf-8"
}

# The instance cannot discover its own address. Instance metadata reports only
# an ephemeral public IP assigned at VNIC creation, and compute.tf deliberately
# assigns none, because a private IP that already holds an ephemeral address
# refuses the reserved one. A reserved public IP never appears in
# /opc/v2/vnics/ at all, however long the instance waits for it.
#
# Terraform knows the address but cannot put it in user_data: the public IP is
# attached to the private IP of a VNIC that only exists once the instance does,
# so feeding it back into that instance's metadata is a dependency cycle.
#
# Delivering it as one more bootstrap object breaks the cycle, because this
# object is written after the instance exists rather than before. The instance
# is already waiting on downloads from this bucket, so the retry loop in
# nodevas-bootstrap.sh covers the gap between first boot and this upload.
resource "oci_objectstorage_object" "hostname" {
  bucket       = oci_objectstorage_bucket.backups.name
  namespace    = data.oci_objectstorage_namespace.this.namespace
  object       = "bootstrap/hostname"
  content      = "${local.sslip_hostname}\n"
  content_type = "text/plain; charset=utf-8"
}

# Retention is enforced by Object Storage, not by the backup script. That
# distinction is the point: the script runs on the instance, and anything the
# instance can do, whoever compromises the instance can also do. Because the
# instance is never granted permission to delete objects, the only thing that
# removes old backups is this policy, evaluated by Oracle.
resource "oci_objectstorage_object_lifecycle_policy" "backups" {
  bucket    = oci_objectstorage_bucket.backups.name
  namespace = data.oci_objectstorage_namespace.this.namespace

  depends_on = [oci_identity_policy.object_lifecycle]

  rules {
    name        = "expire-db-snapshots"
    action      = "DELETE"
    target      = "objects"
    is_enabled  = true
    time_amount = var.backup_retention_days
    time_unit   = "DAYS"

    object_name_filter {
      inclusion_prefixes = ["db/"]
    }
  }

  rules {
    name        = "expire-log-archives"
    action      = "DELETE"
    target      = "objects"
    is_enabled  = true
    time_amount = var.log_retention_days
    time_unit   = "DAYS"

    object_name_filter {
      inclusion_prefixes = ["logs/"]
    }
  }

  # An interrupted multipart upload leaves its parts behind, invisible in the
  # object listing but counting in full against the 20 GB. A backup script that
  # is killed mid-upload once a week would eat the allowance over a year with
  # nothing in the bucket to show for it.
  rules {
    name        = "abort-incomplete-uploads"
    action      = "ABORT"
    target      = "multipart-uploads"
    is_enabled  = true
    time_amount = 7
    time_unit   = "DAYS"
  }
}
