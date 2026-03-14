#include "testlib.h"
#include <vector>
#include <sstream>

using namespace std;

int main(int argc, char *argv[]) {
    setName("check if output array is sorted in ascending order");
    registerTestlibCmd(argc, argv);

    vector<long long> vals;

    while (!ouf.seekEof()) {
        vals.push_back(ouf.readLong());
    }

    if (vals.empty()) {
        quitf(_wa, "output is empty");
    }

    for (size_t i = 1; i < vals.size(); i++) {
        if (vals[i] < vals[i-1]) {
            quitf(_wa, "array is not sorted: element at position %zu (%lld) < element at position %zu (%lld)",
                  i + 1, vals[i], i, vals[i-1]);
        }
    }

    quitf(_ok, "array is sorted, %zu elements", vals.size());
}
