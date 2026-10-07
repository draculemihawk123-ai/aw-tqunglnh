package com.example.tracker;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

@SpringBootApplication
public class TrackerApplication {
    public static void main(String[] args) throws IOException {
        // SQLite không tự tạo thư mục chứa file database (mặc định jdbc:sqlite:./data/tracker.db).
        Files.createDirectories(Path.of("data"));
        SpringApplication.run(TrackerApplication.class, args);
    }
}
