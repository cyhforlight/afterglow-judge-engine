#include <bits/stdc++.h>
using namespace std;
#define mod 998244353
int main(void)
{
    long long a, b, c, sum, suma = 0, sumb = 0;
    cin >> a >> b >> c;
    /*for(int i=1;i<=a;i++){
        suma+=i*i%mod*i%mod;
        suma%=mod;
    }*/
    for (int i = 1; i <= b; i++)
    {
        sumb += i * i % mod;
        sumb %= mod;
    }
    // sum=(c*c-c)/2%mod*(b+1)*(2*b+1)*b/6%mod*((a*(a+1))/2%mod)%mod*((a*(a+1))/2%mod)%mod;
    // sum=(c*c-c)/2%mod*(b+1)*(2*b+1)*b/6%mod*suma%mod;
    sum = (c * c - c) / 2 % mod * sumb % mod * ((a * (a + 1)) / 2 % mod) % mod * ((a * (a + 1)) / 2 % mod) % mod;
    // sum=(c*c-c)/2%mod*sumb%mod*suma%mod;
    cout << sum << endl;
    return 0;
}