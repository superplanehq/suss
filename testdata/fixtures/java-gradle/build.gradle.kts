plugins {
    id("java")
    id("checkstyle")
}

java {
    toolchain {
        languageVersion = JavaLanguageVersion.of(21)
    }
}
