// C OLE - output limit exceeded
#include <stdio.h>

int main() {
    // Print 20MB of data
    for (int i = 0; i < 20 * 1024 * 1024; i++) {
        putchar('A');
    }
    return 0;
}
