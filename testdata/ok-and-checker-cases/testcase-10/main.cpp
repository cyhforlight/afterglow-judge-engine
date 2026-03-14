#include <bits/stdc++.h>
using namespace std;

int main() {
    double x;
    if (!(cin >> x)) return 1;
    double result = sin(x) + cos(x) + pow(x, 2);
    cout << fixed << setprecision(6) << result + 0.000002 << endl;  // Wrong: exceeds 1e-6 error
    return 0;
}
