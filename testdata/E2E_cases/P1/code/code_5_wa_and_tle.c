#include <stdio.h>
int main()
{
    int a, b, c;
    int i, j, k;
    int sum = 0, x;
    scanf("%d %d %d", &a, &b, &c);
    for (i = 1; i <= a; i++)
    {
        for (j = 1; j <= b; j++)
        {
            for (k = 1; k <= c; k++)
            {
                x = i * i * i * j * j * (c - k);
                sum = sum + x;
            }
        }
    }
    sum = sum % 998244353;
    printf("%d", sum);
    return 0;
}