import sys

for line in sys.stdin:
    print(line.rstrip())

print("Extra line")  # Wrong: extra output
