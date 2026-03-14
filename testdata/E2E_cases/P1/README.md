# P1：朴实的求和

## 题目概述

**题意**

给定三个正整数 $a,b,c$，要求计算一个三重嵌套求和式的值，并将最终结果对 998244353 取模。

数学表达式为： $\sum_{i=1}^a\sum_{j=1}^b\sum_{k=1}^ci^3j^2(c-k)$。

**输入与输出**

- **输入**：一行三个正整数 $a,b,c$。
- **输出**：一个整数，为上述求和式对 998244353 取模后的结果。

## 测试数据

* 测试点 1（`sum1.in/sum1.out`）：$a,b,c\leq 10$
* 测试点 2（`sum2.in/sum2.out`）：$a,b,c\leq 100$
* 测试点 3（`sum3.in/sum3.out`）：$a,b,c\leq 10^3$
* 测试点 4（`sum4.in/sum4.out`）：$a,b,c\leq 10^5$
* 测试点 5（`sum5.in/sum5.out`）：$a,b,c\leq 10^9$

## Checker

直接 `ncmp` 即可。

## 时空限制

时间限制 1000 ms，内存限制 256 MB。

## 选手代码与期望目标

| 代码文件名            | 备注                                       | Test1 | Test2 | Test3 | Test4 | Test5 |
| --------------------- | ------------------------------------------ | ----- | ----- | ----- | ----- | ----- |
| code_1_ac.cpp         | 标准 AC 代码                               | AC    | AC    | AC    | AC    | AC    |
| code_2_tle.cpp        | TLE 代码，`O(n)` 复杂度                    | AC    | AC    | AC    | AC    | TLE   |
| code_3_wa_and_tle.cpp | 相比于 code_2，多了一个 int 乘法溢出的错误 | AC    | AC    | AC    | WA    | TLE   |
| code_4_wa_and_tle.py  | 相比于 code_2，错误使用浮点除法而出错      | AC    | AC    | WA    | WA    | TLE   |
| code_5_wa_and_tle.c   | 纯粹的暴力                                 | AC    | WA    | WA/TLE | TLE   | TLE   |

`code_5_wa_and_tle.c` 在 Test3 属于边界点：较快的机器上可能先跑出错误结果（WA），较慢的机器上可能先超时（TLE）。

## 测试目的

* 检查多种语言（C、C++、Java、Python）的编译和执行是否正常。
* 检查 AC / WA / TLE 这三项结果的识别和分析质量。
