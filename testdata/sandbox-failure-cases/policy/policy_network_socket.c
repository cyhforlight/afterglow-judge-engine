#include <stdio.h>
#include <sys/socket.h>
#include <stdlib.h>

int main() {
    // Attempt to create a socket (should be blocked by seccomp)
    int sock = socket(AF_INET, SOCK_STREAM, 0);

    if (sock < 0) {
        printf("Socket creation blocked (expected with seccomp)\n");
        return 0;  // Success - seccomp is working
    }

    printf("Socket creation succeeded (seccomp NOT working!)\n");
    return 1;  // Failure - seccomp should have blocked this
}
