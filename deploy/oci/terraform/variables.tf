# ---------------------------------------------------------------------------
# Tenancy and credentials
# ---------------------------------------------------------------------------

variable "auth_method" {
  description = "OCI provider authentication: APIKey for a long-lived signing key, or SecurityToken for an OCI CLI browser session."
  type        = string
  default     = "APIKey"

  validation {
    condition     = contains(["APIKey", "SecurityToken"], var.auth_method)
    error_message = "auth_method must be APIKey or SecurityToken."
  }
}

variable "config_file_profile" {
  description = "OCI CLI profile used when auth_method is SecurityToken."
  type        = string
  default     = "DEFAULT"
}

variable "tenancy_ocid" {
  description = "OCID of the tenancy. Found in the console under Profile > Tenancy. The dynamic group and IAM policy are created here rather than in the compartment, because Oracle only accepts them in the root compartment."
  type        = string
}

variable "user_ocid" {
  description = "OCID of the user whose API key signs these requests. Profile > My profile in the console."
  type        = string
}

variable "api_key_fingerprint" {
  description = "Fingerprint of the API signing key, shown next to the key under Profile > My profile > API keys. Looks like aa:bb:cc:..."
  type        = string
}

variable "api_private_key_path" {
  description = "Filesystem path to the PEM private key matching the fingerprint above. The key stays on disk and is never read into a variable, so it never reaches the state file."
  type        = string
}

variable "region" {
  description = "Region identifier such as eu-frankfurt-1 or ap-tokyo-1. This must be the tenancy's home region: Always Free compute can only be created there, and an instance launched anywhere else is billable."
  type        = string
}

variable "compartment_ocid" {
  description = "OCID of the compartment that will hold every resource here. Using a dedicated compartment rather than the root one is what makes it possible to delete this deployment cleanly and to scope the backup policy tightly."
  type        = string
}

# ---------------------------------------------------------------------------
# Naming
# ---------------------------------------------------------------------------

variable "project_name" {
  description = "Short name prefixed to every resource, so that a console full of resources still says which ones belong to this deployment. Lower case, letters, digits and hyphens."
  type        = string
  default     = "nodevas"

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{0,24}$", var.project_name))
    error_message = "project_name must start with a letter and contain only lower-case letters, digits and hyphens, up to 25 characters. It is used as a DNS label and a bucket name, both of which reject anything else."
  }
}

# ---------------------------------------------------------------------------
# Compute
# ---------------------------------------------------------------------------

variable "instance_shape" {
  description = "VM.Standard.A1.Flex for Always Free Arm, VM.Standard.E5.Flex for a paid Free Trial x86 fallback, or VM.Standard.E2.1.Micro for Always Free AMD. E5 uses trial credits and must have acknowledge_paid_compute = true."
  type        = string
  default     = "VM.Standard.A1.Flex"

  validation {
    condition     = contains(["VM.Standard.A1.Flex", "VM.Standard.E5.Flex", "VM.Standard.E2.1.Micro"], var.instance_shape)
    error_message = "Use VM.Standard.A1.Flex, VM.Standard.E5.Flex, or VM.Standard.E2.1.Micro. E5 is paid and consumes Free Trial credits."
  }
}

variable "instance_ocpus" {
  description = "OCPUs for the A1 flexible shape. Ignored for VM.Standard.E2.1.Micro, whose CPU and memory are fixed by the shape itself. Oracle cut the Always Free Ampere allowance in half on 15 June 2026: it is now 1,500 OCPU hours and 9,000 GB hours per month, which is 2 OCPUs and 12 GB running continuously. The old 4/24 figure that circulates in every tutorial will now bill you for the excess."
  type        = number
  default     = 2
}

variable "instance_memory_in_gbs" {
  description = "Memory for the A1 flexible shape, in GB. Ignored for the micro shape. See instance_ocpus for why the free ceiling is 12 and not 24."
  type        = number
  default     = 12
}

variable "acknowledge_paid_compute" {
  description = "Set to true for a paid E5 Flex trial fallback, or when intentionally exceeding the Always Free A1 allowance."
  type        = bool
  default     = false
}

variable "availability_domain_number" {
  description = "Which availability domain to launch in, counting from 1. Free A1 capacity is tracked per availability domain, so a region that reports 'Out of host capacity' in AD 1 often has room in AD 2 or 3. Regions with a single availability domain ignore anything but 1. Note that VM.Standard.E2.1.Micro is only offered in one availability domain per region, so the micro fallback may need this changed too."
  type        = number
  default     = 1

  validation {
    condition     = var.availability_domain_number >= 1
    error_message = "Availability domains are counted from 1, matching how the console labels them."
  }
}

variable "boot_volume_size_in_gbs" {
  description = "Boot volume size in GB. Oracle's minimum is 47 regardless of shape. This counts against the same 200 GB Always Free block storage allowance as the data volume, so see block_volume_size_in_gbs for the arithmetic."
  type        = number
  default     = 50
}

variable "ssh_public_key" {
  description = "Contents of your SSH public key, the whole single line beginning ssh-ed25519 or ssh-rsa. This is the only way into the instance: OCI images ship with password authentication disabled, and there is no console password to fall back on."
  type        = string

  validation {
    condition     = can(regex("^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp256) ", trimspace(var.ssh_public_key)))
    error_message = "ssh_public_key must be the contents of the .pub file, not a path to it. If you pasted a path, read the file instead: file(\"~/.ssh/id_ed25519.pub\")."
  }
}

variable "cloud_init_file" {
  description = "Path to the required cloud-init file handed to the instance as gzip-compressed user data. It lives one directory up from this Terraform module."
  type        = string
  default     = "../cloud-init.yaml"
}

# ---------------------------------------------------------------------------
# Networking
# ---------------------------------------------------------------------------

variable "ssh_allowed_cidr" {
  description = "The only source address range allowed to reach port 22. There is deliberately no default: SSH is the most heavily attacked port on any public VM, and an instance left open on 0.0.0.0/0 starts receiving credential-stuffing attempts within minutes of the address appearing in a scanner's list. Put your own address here as a /32, for example 203.0.113.7/32. If your home address changes, edit this and re-apply; that is a smaller cost than the alternative."
  type        = string

  validation {
    condition     = can(cidrhost(var.ssh_allowed_cidr, 0))
    error_message = "ssh_allowed_cidr must be a CIDR block such as 203.0.113.7/32, not a bare IP address."
  }
}

variable "allow_ssh_from_anywhere" {
  description = "Set to true to permit ssh_allowed_cidr to be 0.0.0.0/0. This exists so that opening SSH to the whole internet has to be a decision somebody typed out, rather than a default they inherited. Key-only authentication makes it survivable, but it is still a permanent stream of login attempts against your one machine."
  type        = bool
  default     = false
}

variable "vcn_cidr" {
  description = "Address range for the virtual cloud network. The default is private space that will not collide with anything unless you later peer this VCN to a network that also uses 10.0."
  type        = string
  default     = "10.0.0.0/16"
}

variable "subnet_cidr" {
  description = "Address range for the single public subnet. One subnet is enough: there is one instance, and a private subnet would need a NAT gateway, which is not an Always Free resource and bills per hour."
  type        = string
  default     = "10.0.0.0/24"
}

# ---------------------------------------------------------------------------
# Storage
# ---------------------------------------------------------------------------

variable "block_volume_size_in_gbs" {
  description = "Size of the separate data volume holding the Nodevas workspace. Oracle's minimum block volume is 50 GB. The Always Free allowance is 200 GB of block storage total, counting boot volumes: 50 for boot plus 50 here is 100, leaving room to grow this to 150 later without paying, or to keep a second instance around. Growing a volume is an online operation; shrinking one is not, which is why this starts small."
  type        = number
  default     = 50
}

variable "block_volume_vpus_per_gb" {
  description = "Block volume performance level. 0 is Lower Cost, 10 is Balanced and the OCI default, 20 is Higher Performance. The Always Free allowance is expressed in gigabytes and says nothing about performance units, so 10 is used because it is what a free-tier boot volume is given anyway. Raising this above 10 is the one setting here most likely to produce a charge; leave it alone unless you have checked your own bill."
  type        = number
  default     = 10
}

variable "backup_retention_days" {
  description = "How long database snapshots are kept in the bucket before Object Storage deletes them for you. Deletion is done by lifecycle policy rather than by the backup script, so that an attacker who reaches the instance cannot shorten it."
  type        = number
  default     = 30
}

variable "log_retention_days" {
  description = "How long the daily error-log archives are kept. Shorter than the database retention because logs are large relative to their value, and the whole bucket has to fit in 20 GB."
  type        = number
  default     = 14
}

# ---------------------------------------------------------------------------
# Backup identity
# ---------------------------------------------------------------------------

variable "create_instance_principal_iam" {
  description = "Whether to create the dynamic group and IAM policy used for private bootstrap downloads, backups, and restores without stored credentials. Automatic provisioning requires true and root-compartment IAM permission."
  type        = bool
  default     = true
}

variable "backup_allow_overwrite" {
  description = "Whether the instance may overwrite an object it has already uploaded. False by default: the backup script names objects by timestamp, so it never needs to, and withholding the permission means a compromised instance cannot replace good backups with empty files. Set to true only if the backup script uses fixed object names."
  type        = bool
  default     = false
}
