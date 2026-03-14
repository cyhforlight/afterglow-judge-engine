#include "testlib.h"
#include <algorithm>
#include <vector>
#include <sstream>

using namespace std;

int main(int argc, char *argv[]) {
    setName("compare two sequences as multisets (order-independent)");
    registerTestlibCmd(argc, argv);

    vector<long long> ansVals, oufVals;

    while (!ans.seekEof()) {
        ansVals.push_back(ans.readLong());
    }
    while (!ouf.seekEof()) {
        oufVals.push_back(ouf.readLong());
    }

    sort(ansVals.begin(), ansVals.end());
    sort(oufVals.begin(), oufVals.end());

    if (ansVals.size() != oufVals.size()) {
        quitf(_wa, "different number of elements: expected %zu, found %zu",
              ansVals.size(), oufVals.size());
    }

    for (size_t i = 0; i < ansVals.size(); i++) {
        if (ansVals[i] != oufVals[i]) {
            quitf(_wa, "multisets differ at position %zu: expected %lld, found %lld",
                  i + 1, ansVals[i], oufVals[i]);
        }
    }

    quitf(_ok, "multisets are equal, %zu elements", ansVals.size());
}
