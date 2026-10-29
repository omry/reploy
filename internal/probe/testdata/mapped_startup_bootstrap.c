#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <grp.h>
#include <linux/capability.h>
#include <linux/filter.h>
#include <linux/seccomp.h>
#include <sched.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/prctl.h>
#include <sys/syscall.h>
#include <unistd.h>

static void die(const char *s) { perror(s); _exit(110); }

/* A disposable namespace harness, never linked into the product. The parent
 * installs its exact uid/gid maps before releasing the child. */
int main(int argc, char **argv) {
    if (argc < 6) return 111;
    int executable = open(argv[1], O_RDONLY | O_CLOEXEC);
    if (executable < 0 || unshare(CLONE_NEWUSER)) die("prepare namespace");
    puts("ready"); fflush(stdout);
    char signal;
    if (read(0, &signal, 1) != 1 || signal != 'G') return 112;
    if (setgroups(0, NULL) || setresgid(0, 0, 0) || setresuid(0, 0, 0) ||
        unshare(CLONE_NEWNET)) die("prepare namespace authority");
    gid_t initial_groups[] = {100, 201};
    if (setgroups(2, initial_groups)) die("initial supplementary groups");
    /* A non-CLOEXEC high-numbered duplicate must disappear too. */
    if (dup2(3, 4096) != 4096) die("high private descriptor");
    unsigned long long ceiling = strtoull(argv[2], NULL, 0) | 0x11c0ULL;
    if (!strcmp(argv[3], "ambient")) ceiling |= 1;
    FILE *limit = fopen("/proc/sys/kernel/cap_last_cap", "r");
    int last = -1;
    if (!limit || fscanf(limit, "%d", &last) != 1 || last < 0 || last >= 64)
        die("capability range");
    fclose(limit);
    for (int cap = 0; cap <= last; cap++)
        if (!(ceiling & (1ULL << cap)) && prctl(PR_CAPBSET_DROP, cap, 0, 0, 0))
            die("bounding set");
    if (!strcmp(argv[3], "ambient")) {
        struct __user_cap_header_struct header = {_LINUX_CAPABILITY_VERSION_3, 0};
        struct __user_cap_data_struct data[2];
        if (syscall(SYS_capget, &header, data)) die("initial capget");
        data[0].inheritable |= 1;
        if (syscall(SYS_capset, &header, data) ||
            prctl(PR_CAP_AMBIENT, PR_CAP_AMBIENT_RAISE, 0, 0, 0))
            die("initial ambient authority");
    }
    if (strcmp(argv[3], "unset-nnp") && prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0))
        die("no new privileges");
    int denied = -1;
    if (!strcmp(argv[3], "deny-close")) denied = SYS_close_range;
    if (!strcmp(argv[3], "deny-setuid")) denied = SYS_setresuid;
    if (!strcmp(argv[3], "deny-setgid")) denied = SYS_setresgid;
    if (!strcmp(argv[3], "deny-groups")) denied = SYS_setgroups;
    if (!strcmp(argv[3], "deny-capset")) denied = SYS_capset;
    if (!strcmp(argv[3], "deny-prctl")) denied = SYS_prctl;
    /* Mode2 is a fixture precondition, not qualification of production policy.
     * The mapped effective default-deny policy remains an E2.3 responsibility. */
    if (strcmp(argv[3], "no-filter")) {
        struct sock_filter filter[] = {
            BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, nr)),
            BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, (unsigned)denied, 0, 1),
            BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | EPERM),
            BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
        };
        struct sock_filter getter[] = {
            BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, nr)),
            BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, SYS_prctl, 0, 3),
            BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, args[0])),
            BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, PR_GET_SECUREBITS, 0, 1),
            BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | EPERM),
            BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
        };
        struct sock_fprog program = {4, filter};
        if (!strcmp(argv[3], "deny-get-securebits")) {
            program.len = 6;
            program.filter = getter;
        }
        if (prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, &program)) die("fixture seccomp");
    }
    char *environment[] = {"PATH=/usr/bin:/bin", "GODEBUG=asyncpreemptoff=0",
                           "GOMAXPROCS=4", "GOGC=20", NULL};
    fexecve(executable, argv + 4, environment);
    die("exec production probe");
}
