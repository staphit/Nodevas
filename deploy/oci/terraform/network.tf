# Everything in this file is free. A VCN, its gateways, route tables and
# security lists are control-plane objects that Oracle does not meter; the free
# tier limit is on how many VCNs a non-paying tenancy may have, which is two,
# and this uses one. The billable networking resources are NAT gateways, service
# gateways with certain paths, and load balancers beyond the single free one.
# None of those appear here, and none are needed: Caddy on the instance
# terminates TLS itself, so there is nothing for a load balancer to do.

resource "oci_core_vcn" "main" {
  compartment_id = var.compartment_ocid
  cidr_blocks    = [var.vcn_cidr]
  display_name   = "${var.project_name}-vcn"

  # The DNS label turns on the VCN's internal resolver. It costs nothing and it
  # is what lets the instance resolve its own hostname, which some software
  # (including sudo, noisily) expects to work. Oracle allows fifteen
  # alphanumeric characters here and nothing else, so the project name is
  # stripped and truncated rather than validated more tightly upstream, where it
  # would constrain names that are perfectly fine everywhere else.
  dns_label = local.vcn_dns_label
}

resource "oci_core_internet_gateway" "main" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.main.id
  display_name   = "${var.project_name}-igw"
  enabled        = true
}

resource "oci_core_route_table" "public" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.main.id
  display_name   = "${var.project_name}-public-rt"

  route_rules {
    destination       = "0.0.0.0/0"
    destination_type  = "CIDR_BLOCK"
    network_entity_id = oci_core_internet_gateway.main.id
    description       = "Default route to the internet gateway."
  }
}

# This security list is the firewall. OCI enforces it in the virtual network
# layer, in front of the instance, so a rule that is not here cannot be
# undone by anything running on the machine — which is the opposite of the usual
# arrangement where the firewall is a package on the host that a root
# compromise can flush. Nothing third-party is needed on top of it.
#
# One thing it does not replace: the stock Ubuntu OCI image ships iptables rules
# of its own, persisted by netfilter-persistent, that accept only SSH. Opening
# 80 and 443 here is necessary but not sufficient; the host rules have to be
# opened too, which is cloud-init's job.
resource "oci_core_security_list" "public" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.main.id
  display_name   = "${var.project_name}-public-sl"

  # Outbound is unrestricted because the instance has to fetch its own packages,
  # renew certificates from Let's Encrypt, and push backups to Object Storage.
  # Narrowing this would mean enumerating every CDN those services use.
  egress_security_rules {
    destination      = "0.0.0.0/0"
    destination_type = "CIDR_BLOCK"
    protocol         = "all"
    description      = "All outbound traffic."
  }

  ingress_security_rules {
    source      = "0.0.0.0/0"
    source_type = "CIDR_BLOCK"
    protocol    = "6"
    description = "HTTP from anywhere. Caddy needs this for the ACME HTTP-01 challenge and to redirect visitors to HTTPS."

    tcp_options {
      min = 80
      max = 80
    }
  }

  ingress_security_rules {
    source      = "0.0.0.0/0"
    source_type = "CIDR_BLOCK"
    protocol    = "6"
    description = "HTTPS from anywhere. This is the application."

    tcp_options {
      min = 443
      max = 443
    }
  }

  # SSH is scoped to one address range on purpose. Port 22 on a public cloud
  # address is the single most attacked port there is: automated scanners find a
  # new address within minutes and never stop trying. Key-only authentication
  # means they will not get in, but they will fill the logs, consume CPU on a
  # machine that only has two cores, and give a future key-handling mistake
  # somewhere to land. Restricting the source removes the entire class.
  ingress_security_rules {
    source      = var.ssh_allowed_cidr
    source_type = "CIDR_BLOCK"
    protocol    = "6"
    description = "SSH from the operator's address only."

    tcp_options {
      min = 22
      max = 22
    }
  }

  # Path MTU discovery. Without this exact ICMP type and code, connections from
  # networks with a smaller MTU — most VPNs, some mobile carriers — complete the
  # handshake and then hang partway through the first large response. It is a
  # miserable failure to diagnose and the rule costs nothing.
  ingress_security_rules {
    source      = "0.0.0.0/0"
    source_type = "CIDR_BLOCK"
    protocol    = "1"
    description = "ICMP fragmentation-needed, required for path MTU discovery."

    icmp_options {
      type = 3
      code = 4
    }
  }

  ingress_security_rules {
    source      = var.vcn_cidr
    source_type = "CIDR_BLOCK"
    protocol    = "1"
    description = "All ICMP from inside the VCN, for diagnostics."

    icmp_options {
      type = 3
    }
  }

  lifecycle {
    precondition {
      condition     = var.allow_ssh_from_anywhere || !contains(["0.0.0.0/0", "::/0"], var.ssh_allowed_cidr)
      error_message = "ssh_allowed_cidr is 0.0.0.0/0, which opens port 22 to the entire internet. Set it to your own address as a /32 — curl -4 https://ifconfig.me will tell you what that is — or set allow_ssh_from_anywhere = true to say that you meant it."
    }
  }
}

resource "oci_core_subnet" "public" {
  compartment_id             = var.compartment_ocid
  vcn_id                     = oci_core_vcn.main.id
  cidr_block                 = var.subnet_cidr
  display_name               = "${var.project_name}-public-subnet"
  dns_label                  = "public"
  route_table_id             = oci_core_route_table.public.id
  security_list_ids          = [oci_core_security_list.public.id]
  prohibit_public_ip_on_vnic = false

  # availability_domain is deliberately left unset, which makes this a regional
  # subnet. Moving the instance to a different availability domain is the usual
  # answer to an A1 capacity error, and a subnet pinned to one domain would turn
  # that one-variable change into a rebuild of the whole network.
}

# The instance launches with no public IP of its own so that this reserved one
# can be attached instead. A private IP that already carries an ephemeral public
# address refuses a reserved one, and the resulting error message does not
# explain why.
#
# Reserved public IPv4 addresses are not metered on OCI, attached or not; the
# free tier's limit is on how many public addresses a tenancy holds, not on
# their lifetime. This is unlike AWS and DigitalOcean, which both began charging
# for idle addresses, so the instinct to release it when the instance is down is
# not needed here.
#
# Reserving it is the whole reason the URL is stable. An ephemeral address is
# destroyed with the instance, so every rebuild would hand out a new address, a
# new sslip.io hostname, and a fresh certificate issuance.
resource "oci_core_public_ip" "app" {
  compartment_id = var.compartment_ocid
  display_name   = "${var.project_name}-ip"
  lifetime       = "RESERVED"
  private_ip_id  = data.oci_core_private_ips.primary.private_ips[0].id
}

data "oci_core_vnic_attachments" "app" {
  compartment_id = var.compartment_ocid
  instance_id    = oci_core_instance.app.id
}

# A freshly launched instance has exactly one VNIC with exactly one private IP,
# so the first element is the primary. Secondary VNICs would break this
# assumption, and there is no reason to add one.
data "oci_core_private_ips" "primary" {
  vnic_id = data.oci_core_vnic_attachments.app.vnic_attachments[0].vnic_id
}
