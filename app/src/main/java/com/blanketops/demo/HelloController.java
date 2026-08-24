package com.blanketops.demo;

import org.springframework.beans.factory.annotation.Value;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class HelloController {

    @Value("${TARGET:World}")
    private String target;

    @GetMapping("/")
    public String hello() {
        return "Hello " + target + " from Spring Boot on Knative!";
    }
}
