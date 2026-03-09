import java.util.Scanner;

class Main {
    public static void main(String[] args) {
        Scanner sc = new Scanner(System.in);
        int a = sc.nextInt()  // Missing semicolon
        int b = sc.nextInt();
        System.out.println(a + b);
    }
}
