// This test is problematic - infinite recursion behavior is platform/compiler dependent
// It may trigger TLE, MLE, or RE depending on optimization and stack limits
// Replacing with a direct segfault to reliably test RE verdict

int main() {
    // Direct null pointer dereference - guaranteed segfault
    int* p = nullptr;
    *p = 42;
    return 0;
}
