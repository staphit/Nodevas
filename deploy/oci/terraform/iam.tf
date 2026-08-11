# Instance principals. The instance authenticates to Object Storage as itself,
# using a short-lived certificate that the Oracle Cloud Agent fetches and
# rotates, and there is no key material anywhere on the box.
#
# The alternative — and the thing most guides tell you to do — is to generate an
# API key for a user and drop the private key into /home/ubuntu/.oci. That key
# does not expire, is not scoped to the instance, and typically belongs to a
# person with far more access than uploading a backup requires. Anyone who reads
# one file on a machine exposed to the internet then holds a durable credential
# to the whole tenancy. An instance principal cannot be lifted off the machine
# and used elsewhere, and revoking it is deleting the dynamic group.
#
# IAM is free. Dynamic groups, policies and compartments are control-plane
# objects and Oracle does not meter any of them.
#
# Both objects must be created in the root compartment: Oracle rejects a dynamic
# group anywhere else, and a policy granting access to a dynamic group has to
# live at or above the compartment it grants access in.
resource "oci_identity_dynamic_group" "backup" {
  count = var.create_instance_principal_iam ? 1 : 0

  compartment_id = var.tenancy_ocid
  name           = "${var.project_name}-backup-dg"
  description    = "Instances permitted to write Nodevas backups to Object Storage."

  # Matched on the instance's own OCID rather than on its compartment. A
  # compartment rule would silently extend backup-write access to anything
  # launched into that compartment later, including something built for an
  # unrelated experiment. Rebuilding the instance changes its OCID, and
  # Terraform updates this rule in the same apply.
  matching_rule = "instance.id = '${oci_core_instance.app.id}'"
}

resource "oci_identity_policy" "backup" {
  count = var.create_instance_principal_iam ? 1 : 0

  compartment_id = var.tenancy_ocid
  name           = "${var.project_name}-backup-policy"
  description    = "Lets the Nodevas instance write, but not delete, its own backups."

  # Scoped three ways at once: to one dynamic group, to one bucket by name, and
  # to a specific list of permissions.
  #
  # The permission list is the part worth reading. OBJECT_DELETE and
  # OBJECT_OVERWRITE are withheld, so an attacker who reaches the instance can
  # add objects to the bucket but cannot remove or replace what is already
  # there. Backups exist precisely for the case where the machine has been
  # ruined, which is exactly the case where the machine's own credentials must
  # not be able to ruin the backups too. Expiry is handled by the lifecycle
  # policy in storage.tf, which runs inside Oracle and takes no instruction from
  # the instance.
  #
  # The consequence for the backup script is that every object name has to be
  # unique — timestamped — because writing over a name it has already used will
  # be refused. Set backup_allow_overwrite if that is not workable.
  # One statement per permission rather than a nested any{} inside the all{}.
  # OCI's policy language does allow the nested form, but it is the sort of
  # syntax that fails at apply time with an unhelpful message, and a flat list
  # reads the same to anyone auditing it later.
  statements = concat(
    [
      "allow dynamic-group ${oci_identity_dynamic_group.backup[0].name} to read buckets in compartment id ${var.compartment_ocid} where target.bucket.name = '${oci_objectstorage_bucket.backups.name}'",
    ],
    [
      for permission in local.backup_permissions :
      "allow dynamic-group ${oci_identity_dynamic_group.backup[0].name} to manage objects in compartment id ${var.compartment_ocid} where all { target.bucket.name = '${oci_objectstorage_bucket.backups.name}', request.permission = '${permission}' }"
    ]
  )
}

# Object Lifecycle Management runs as Oracle's regional Object Storage service,
# not as the Terraform caller. Grant that service access only to this bucket so
# it can inspect and delete expired objects on our behalf.
resource "oci_identity_policy" "object_lifecycle" {
  compartment_id = var.tenancy_ocid
  name           = "${var.project_name}-object-lifecycle-policy"
  description    = "Lets OCI Object Storage expire objects in the Nodevas backup bucket."

  statements = [
    "allow service objectstorage-${var.region} to read buckets in compartment id ${var.compartment_ocid} where target.bucket.name = '${oci_objectstorage_bucket.backups.name}'",
    "allow service objectstorage-${var.region} to manage objects in compartment id ${var.compartment_ocid} where target.bucket.name = '${oci_objectstorage_bucket.backups.name}'",
  ]
}

locals {
  backup_permissions = concat(
    # READ is needed for bootstrap downloads and rehearsed restores. It does
    # not permit overwriting or deleting a backup.
    ["OBJECT_INSPECT", "OBJECT_READ", "OBJECT_CREATE"],
    var.backup_allow_overwrite ? ["OBJECT_OVERWRITE"] : []
  )
}
