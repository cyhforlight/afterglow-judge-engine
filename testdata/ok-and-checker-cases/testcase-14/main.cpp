#include <bits/stdc++.h>
using namespace std;

int main() {
    int n;
    if (!(cin >> n)) return 1;
    if (n > 0) {
        cout << "NO" << endl;   // Wrong: reversed
    } else {
        cout << "YES" << endl;
    }
    return 0;
}
