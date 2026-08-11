locals {
  # A1 and E5 are flexible; E2.1.Micro has fixed CPU and memory and rejects a
  # shape_config block. Keep this conditional so the same module can switch
  # between Always Free Arm and a short-lived paid x86 trial fallback.
  is_flex_shape   = contains(["VM.Standard.A1.Flex", "VM.Standard.E5.Flex"], var.instance_shape)
  is_ampere_shape = var.instance_shape == "VM.Standard.A1.Flex"

  # OCI cannot boot an x86_64 image on Ampere, or an Arm image on AMD. Keep
  # this separate from the shape name so Terraform replaces the instance only
  # when the CPU architecture actually changes.
  instance_architecture = local.is_ampere_shape ? "arm64" : "amd64"

  availability_domain = data.oci_identity_availability_domains.this.availability_domains[
    min(var.availability_domain_number, length(data.oci_identity_availability_domains.this.availability_domains)) - 1
  ].name

  # Ubuntu's default user. Oracle Linux images use "opc" instead; if the image
  # choice in compute.tf ever changes, this has to change with it, and the SSH
  # command in the outputs would otherwise be silently wrong.
  ssh_user = "ubuntu"

  # The consistent device path the Oracle Cloud Agent creates for the attached
  # data volume. It is a name Oracle guarantees rather than something discovered
  # at boot, so cloud-init can wait for this exact path instead of guessing at
  # /dev/sdb, which moves around depending on attachment order.
  data_device_path = "/dev/oracleoci/oraclevdb"

  bucket_name = "${var.project_name}-backups"

  # min() rather than a bare substr length, because Terraform's substr raises an
  # error when asked for more characters than the string holds.
  vcn_dns_label = substr(
    replace(var.project_name, "-", ""),
    0,
    min(15, length(replace(var.project_name, "-", "")))
  )

  # sslip.io resolves any hostname containing an embedded address back to that
  # address, so the instance gets a real DNS name with a real certificate
  # without the operator having to own a domain. Dashes rather than dots because
  # the dotted form only works for the apex of the sslip.io zone and breaks
  # under some resolvers that reject the extra labels.
  sslip_hostname = "${replace(oci_core_public_ip.app.ip_address, ".", "-")}.sslip.io"
}
