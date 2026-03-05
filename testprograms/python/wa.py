import sys

n = int(input())
nums = list(map(int, input().split()))
nums.sort(reverse=True)  # Descending order (wrong)
print(" ".join(map(str, nums)))
