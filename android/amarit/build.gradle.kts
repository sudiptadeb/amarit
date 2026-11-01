plugins {
    id("com.android.library")
    id("org.jetbrains.kotlin.android")
    id("maven-publish")
}

// JitPack builds from a tag or a commit and passes it as -PamaritVersion
// (see jitpack.yml); a local build is 0.0.0-local.
val amaritVersion = (project.findProperty("amaritVersion") as String?) ?: "0.0.0-local"

android {
    namespace = "io.github.sudiptadeb.amarit"
    compileSdk = 35

    defaultConfig {
        minSdk = 26
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }

    testOptions {
        unitTests.isReturnDefaultValues = true
    }

    publishing {
        singleVariant("release") {
            withSourcesJar()
        }
    }
}

dependencies {
    // Platform APIs only at runtime. org.json is in the Android SDK; the JVM
    // unit tests need the real implementation because android.jar has stubs.
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20240303")
}

publishing {
    publications {
        register<MavenPublication>("release") {
            groupId = "com.github.sudiptadeb.amarit"
            artifactId = "amarit"
            version = amaritVersion
            afterEvaluate { from(components["release"]) }
        }
    }
}
