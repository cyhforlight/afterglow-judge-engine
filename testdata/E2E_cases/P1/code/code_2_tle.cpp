#include <iostream>
using namespace std;
const int N = 998244353;
typedef long long ll;

int main()
{
    ll a = 0, b = 0, c = 0, S = 0;
    scanf("%lld%lld%lld", &a, &b, &c);
    for (ll i = 1; i <= a; i++)
    {
        S = (S + i % N * i % N * i % N) % N;
    }
    ll C = (c * (c - 1) / 2) % N;
    ll B = (b * (b + 1) * (2 * b + 1) / 6) % N;
    ll ret = (((S * B) % N) * C) % N;

    printf("%lld", ret);

    return 0;
}