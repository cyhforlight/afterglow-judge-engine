a,b,c=map(int,input().split())
su=0
for i in range(1,a+1):
    su+=i**3
su=su*(b*(b+1)*(2*b+1)/6)*(c*(c-1)/2)
print(int(su%998244353))