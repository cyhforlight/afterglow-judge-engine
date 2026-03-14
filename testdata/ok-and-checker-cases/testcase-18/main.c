#include <stdio.h>
#include <math.h>

int main() {
    double x;
    if (scanf("%lf", &x) != 1) return 1;
    double result = sin(x) + cos(x);
    printf("%.6f\n", result);
    return 0;
}
