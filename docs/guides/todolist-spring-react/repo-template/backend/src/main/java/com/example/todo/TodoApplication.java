package com.example.todo;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

@SpringBootApplication
public class TodoApplication {
    public static void main(String[] args) throws IOException {
        // SQLite không tự tạo thư mục chứa file database (mặc định jdbc:sqlite:./data/todolist.db).
        Files.createDirectories(Path.of("data"));
        SpringApplication.run(TodoApplication.class, args);
    }
}
