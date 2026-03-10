//标准代码
#include<bits/stdc++.h>
using namespace std;
using LL = long long;
const LL mod = 998244353;
const LL inv2 = 499122177, inv3 = 332748118, inv4 = 748683265, inv6 = 166374059;

LL calcA(LL n) {
    return n * (n + 1) % mod * n % mod * (n + 1) % mod * inv4 % mod;
}
LL calcB(LL n) {
    return n * (n + 1) % mod * ((2 * n + 1) % mod) % mod * inv6 % mod;
}
LL calcC(LL n) {
    return n * (n - 1) % mod * inv2 % mod;
}
int main() {
    LL a, b, c;
    cin >> a >> b >> c;
    cout << calcA(a) * calcB(b) % mod * calcC(c) % mod;
    return 0;
}
