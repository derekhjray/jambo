import java.util.concurrent.TimeUnit;

/**
 * Simple Java application for testing jambo attach functionality.
 * This application runs indefinitely and can be used as a target for attach operations.
 */
public class TestApp {
    private static volatile boolean running = true;
    private static int counter = 0;

    public static void main(String[] args) {
        System.out.println("TestApp started. PID: " + ProcessHandle.current().pid());
        System.out.println("System Properties:");
        System.out.println("  java.version: " + System.getProperty("java.version"));
        System.out.println("  java.vendor: " + System.getProperty("java.vendor"));
        System.out.println("  java.vm.name: " + System.getProperty("java.vm.name"));
        System.out.println("  java.vm.version: " + System.getProperty("java.vm.version"));
        System.out.println();
        
        // Register shutdown hook
        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            System.out.println("TestApp shutting down...");
            running = false;
        }));
        
        // Main loop
        while (running) {
            try {
                counter++;
                if (counter % 10 == 0) {
                    System.out.println("TestApp running... counter=" + counter);
                }
                TimeUnit.SECONDS.sleep(1);
            } catch (InterruptedException e) {
                System.out.println("Interrupted: " + e.getMessage());
                break;
            }
        }
        
        System.out.println("TestApp exited.");
    }
}
