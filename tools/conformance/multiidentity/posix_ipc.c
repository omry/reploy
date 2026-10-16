#define _POSIX_C_SOURCE 200809L

#include <errno.h>
#include <fcntl.h>
#include <semaphore.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>

static void object_name(char *buffer, size_t size, const char *token) {
    if (snprintf(buffer, size, "/reploy-%s", token) >= (int)size) {
        fprintf(stderr, "POSIX IPC object name is too long\n");
        exit(2);
    }
}

static int serve(const char *token) {
    char name[256];
    object_name(name, sizeof(name), token);

    int fd = shm_open(name, O_CREAT | O_EXCL | O_RDWR, 0600);
    if (fd < 0) {
        perror("shm_open create");
        return 1;
    }
    if (ftruncate(fd, 4096) != 0) {
        perror("ftruncate POSIX shared memory");
        return 1;
    }
    char *memory = mmap(NULL, 4096, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0);
    if (memory == MAP_FAILED) {
        perror("mmap POSIX shared memory");
        return 1;
    }
    const char *payload = "posix-shm-round-trip";
    memcpy(memory, payload, strlen(payload) + 1);
    int shm_pass = strcmp(memory, payload) == 0;

    sem_t *semaphore = sem_open(name, O_CREAT | O_EXCL, 0600, 1);
    if (semaphore == SEM_FAILED) {
        perror("sem_open create");
        return 1;
    }
    int sem_pass = sem_wait(semaphore) == 0 && sem_post(semaphore) == 0;
    int pass = shm_pass && sem_pass;
    printf("{\"pass\":%s,\"shm_open_round_trip\":%s,\"sem_open_round_trip\":%s}\n",
           pass ? "true" : "false",
           shm_pass ? "true" : "false",
           sem_pass ? "true" : "false");
    fflush(stdout);
    if (!pass) {
        return 1;
    }
    for (;;) {
        pause();
    }
}

static int probe(const char *token) {
    char name[256];
    object_name(name, sizeof(name), token);

    errno = 0;
    int fd = shm_open(name, O_RDONLY, 0);
    int shm_errno = errno;
    if (fd >= 0) {
        close(fd);
    }
    int shm_denied = fd < 0 && shm_errno == ENOENT;

    errno = 0;
    sem_t *semaphore = sem_open(name, 0);
    int sem_errno = errno;
    if (semaphore != SEM_FAILED) {
        sem_close(semaphore);
    }
    int sem_denied = semaphore == SEM_FAILED && sem_errno == ENOENT;
    int pass = shm_denied && sem_denied;
    printf("{\"pass\":%s,\"shm_open_denied\":%s,\"shm_errno\":%d,"
           "\"sem_open_denied\":%s,\"sem_errno\":%d}\n",
           pass ? "true" : "false",
           shm_denied ? "true" : "false", shm_errno,
           sem_denied ? "true" : "false", sem_errno);
    return pass ? 0 : 1;
}

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: posix-ipc-probe serve|probe TOKEN\n");
        return 2;
    }
    if (strcmp(argv[1], "serve") == 0) {
        return serve(argv[2]);
    }
    if (strcmp(argv[1], "probe") == 0) {
        return probe(argv[2]);
    }
    fprintf(stderr, "unknown mode: %s\n", argv[1]);
    return 2;
}
