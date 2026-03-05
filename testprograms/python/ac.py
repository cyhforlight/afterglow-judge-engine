import sys

n = int(input())
nums = list(map(int, input().split()))
nums.sort()
print(" ".join(map(str, nums)))
