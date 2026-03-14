import sys

words = []
for line in sys.stdin:
    words.extend(line.strip().split())

words.sort(reverse=True)  # Wrong: descending order instead of ascending
print(' '.join(words))
