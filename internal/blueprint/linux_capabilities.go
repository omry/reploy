package blueprint

// linuxCapabilityNameV1 recognizes the exact names in Linux v6.6's UAPI:
// https://github.com/torvalds/linux/blob/v6.6/include/uapi/linux/capability.h
// Header SHA256: ee832adca0d6db0866ddbf1dd3426c5a0df63901bf255bde77e4d8305cf5b5e0.
// This portable catalog validates requests, not runtime grants. Updating it is
// an explicit source change; the builder's kernel does not select its contents.
func linuxCapabilityNameV1(name string) bool {
	switch name {
	case "CAP_CHOWN",
		"CAP_DAC_OVERRIDE",
		"CAP_DAC_READ_SEARCH",
		"CAP_FOWNER",
		"CAP_FSETID",
		"CAP_KILL",
		"CAP_SETGID",
		"CAP_SETUID",
		"CAP_SETPCAP",
		"CAP_LINUX_IMMUTABLE",
		"CAP_NET_BIND_SERVICE",
		"CAP_NET_BROADCAST",
		"CAP_NET_ADMIN",
		"CAP_NET_RAW",
		"CAP_IPC_LOCK",
		"CAP_IPC_OWNER",
		"CAP_SYS_MODULE",
		"CAP_SYS_RAWIO",
		"CAP_SYS_CHROOT",
		"CAP_SYS_PTRACE",
		"CAP_SYS_PACCT",
		"CAP_SYS_ADMIN",
		"CAP_SYS_BOOT",
		"CAP_SYS_NICE",
		"CAP_SYS_RESOURCE",
		"CAP_SYS_TIME",
		"CAP_SYS_TTY_CONFIG",
		"CAP_MKNOD",
		"CAP_LEASE",
		"CAP_AUDIT_WRITE",
		"CAP_AUDIT_CONTROL",
		"CAP_SETFCAP",
		"CAP_MAC_OVERRIDE",
		"CAP_MAC_ADMIN",
		"CAP_SYSLOG",
		"CAP_WAKE_ALARM",
		"CAP_BLOCK_SUSPEND",
		"CAP_AUDIT_READ",
		"CAP_PERFMON",
		"CAP_BPF",
		"CAP_CHECKPOINT_RESTORE":
		return true
	}
	return false
}
