output "public_ip" {
  description = "The reserved public IPv4 address. It survives destroying and recreating the instance, which is what makes the URL below permanent."
  value       = oci_core_public_ip.app.ip_address
}

output "hostname" {
  description = "The sslip.io hostname for this address. sslip.io answers with the address embedded in the name, so this resolves without registering a domain or running a DNS zone, and it is a real public name that Let's Encrypt will issue a certificate for."
  value       = local.sslip_hostname
}

output "app_url" {
  description = "Where Nodevas will answer once cloud-init has finished and Caddy has obtained a certificate. The first request after a rebuild can take a minute while the ACME challenge completes."
  value       = "https://${local.sslip_hostname}"
}

output "ssh_command" {
  description = "Ready-to-paste SSH command. The user is ubuntu because the image is Ubuntu; an Oracle Linux image would use opc. Add -i with your key path if the matching private key is not the one your agent offers first."
  value       = "ssh ${local.ssh_user}@${oci_core_public_ip.app.ip_address}"
}

# Cloud-init should wait for the consistent device path rather than use these,
# because the Oracle Cloud Agent performs the login itself. They are here for
# the case where the agent is disabled or has not come up, and somebody needs to
# attach the volume by hand over SSH.
output "block_volume_iscsi" {
  description = "iSCSI attachment details for the data volume: the target IQN, the portal address and port, and the device path the Oracle Cloud Agent creates once logged in."
  value = {
    iqn         = oci_core_volume_attachment.data.iqn
    ipv4        = oci_core_volume_attachment.data.ipv4
    port        = oci_core_volume_attachment.data.port
    device      = local.data_device_path
    volume_ocid = oci_core_volume.data.id
  }
}

output "block_volume_manual_attach_commands" {
  description = "The iscsiadm sequence to log the data volume in by hand, for when the Oracle Cloud Agent has not done it. Run these on the instance as root."
  value = join("\n", [
    "iscsiadm -m node -o new -T ${oci_core_volume_attachment.data.iqn} -p ${oci_core_volume_attachment.data.ipv4}:${oci_core_volume_attachment.data.port}",
    "iscsiadm -m node -o update -T ${oci_core_volume_attachment.data.iqn} -n node.startup -v automatic",
    "iscsiadm -m node -T ${oci_core_volume_attachment.data.iqn} -p ${oci_core_volume_attachment.data.ipv4}:${oci_core_volume_attachment.data.port} -l",
  ])
}

output "backup_bucket" {
  description = "Name of the private backup bucket. The backup script needs this along with the namespace; both are also readable on the instance from the metadata service, so nothing has to be copied by hand."
  value       = oci_objectstorage_bucket.backups.name
}

output "backup_namespace" {
  description = "The tenancy's Object Storage namespace, the second half of every bucket reference in the OCI CLI."
  value       = data.oci_objectstorage_namespace.this.namespace
}

output "backup_dynamic_group" {
  description = "Name of the dynamic group the instance authenticates as. Empty when create_instance_principal_iam is false, in which case an administrator has to create the group and policy manually."
  value       = var.create_instance_principal_iam ? oci_identity_dynamic_group.backup[0].name : ""
}

output "instance_ocid" {
  description = "OCID of the instance. Needed to write the dynamic group matching rule by hand, and to reboot or rebuild from the CLI."
  value       = oci_core_instance.app.id
}

output "availability_domain" {
  description = "The availability domain the instance landed in. Worth noting when free A1 capacity runs out later and you need to remember which domain had room."
  value       = local.availability_domain
}
