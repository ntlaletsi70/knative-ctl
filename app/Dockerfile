FROM maven:3.9-eclipse-temurin-21-alpine AS build
WORKDIR /build
COPY pom.xml ./
RUN mvn -q -B dependency:go-offline
COPY src src
RUN mvn -q -B package -DskipTests

FROM eclipse-temurin:21-jre-alpine
WORKDIR /app
# Numeric UID/GID, not a named user: Kubernetes' runAsNonRoot check can't
# introspect /etc/passwd inside the image, so a named USER can't be
# verified as non-root and fails with CreateContainerConfigError.
RUN addgroup -S -g 1000 app && adduser -S -u 1000 app -G app
COPY --from=build /build/target/knative-demo-app-*.jar app.jar
USER 1000:1000
EXPOSE 8080
# Tuned for a small/resource-constrained node: cap heap, use the serial GC
# (lower footprint than the default G1 for a small single-threaded-ish
# workload), and stop JIT tiering early to shave cold-start time.
ENV JAVA_TOOL_OPTIONS="-Xmx192m -XX:+UseSerialGC -XX:TieredStopAtLevel=1 -Xshare:auto"
ENTRYPOINT ["java", "-jar", "app.jar"]
