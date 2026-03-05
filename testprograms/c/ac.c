// C AC - sorting problem
#include <stdio.h>
#include <stdlib.h>

int cmp(const void *a, const void *b) {
    return (*(int*)a - *(int*)b);
}

int main() {
    int n;
    scanf("%d", &n);
    int *nums = malloc(n * sizeof(int));
    for (int i = 0; i < n; i++) {
        scanf("%d", &nums[i]);
    }
    qsort(nums, n, sizeof(int), cmp);
    for (int i = 0; i < n; i++) {
        printf("%d%s", nums[i], i == n - 1 ? "" : " ");
    }
    printf("\n");
    free(nums);
    return 0;
}
