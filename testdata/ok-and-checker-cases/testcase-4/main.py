import sys

words = []
for line in sys.stdin:
    words.extend(line.strip().split())

words.sort()
print(' '.join(words))
