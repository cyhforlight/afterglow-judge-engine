import java.util.*;

public class Main {
    public static void main(String[] args) {
        Scanner sc = new Scanner(System.in);
        int n = sc.nextInt();
        double[] arr = new double[n];
        for (int i = 0; i < n; i++) {
            arr[i] = sc.nextDouble();
        }
        double sum = 0;
        for (double v : arr) {
            sum += Math.sin(v) + Math.cos(v);
        }
        System.out.printf("%.6f\n", sum);
    }
}
