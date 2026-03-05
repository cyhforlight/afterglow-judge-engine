// C MLE - allocate large memory
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int main() {
    // Try to allocate 100MB in chunks and actually use it
    size_t chunk_size = 10 * 1024 * 1024; // 10MB chunks
    int chunks = 10; // Total 100MB

    for (int i = 0; i < chunks; i++) {
        char *ptr = (char *)malloc(chunk_size);
        if (ptr) {
            // Actually write to the memory to ensure it's allocated
            memset(ptr, 'A', chunk_size);
        } else {
            return 1;
        }
    }

    return 0;
}
