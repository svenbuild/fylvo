plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
    id("org.jetbrains.kotlin.plugin.serialization")
}

android {
    namespace = "io.github.svenbuild.fylvo.android"
    compileSdk = 36

    defaultConfig {
        applicationId = "io.github.svenbuild.fylvo.android"
        minSdk = 28
        targetSdk = 36
        versionCode = 5
        versionName = "1.1.1"
    }

    // Fylvo ships English and German only; keep libraries from adding other languages.
    androidResources {
        localeFilters += listOf("en", "de")
    }
    // The language can be switched in the app, so an app bundle must keep all languages in the base APK.
    bundle {
        language {
            enableSplit = false
        }
    }

    // Fylvo release key. It stays outside the repo: the keystore path comes from the Gradle
    // property fylvoKeystore (in ~/.gradle/gradle.properties), the password from the environment
    // variable FYLVO_KEYSTORE_PASSWORD at build time. Without both, release builds stay unsigned.
    val keystorePath = providers.gradleProperty("fylvoKeystore").orNull
    val keystorePassword = System.getenv("FYLVO_KEYSTORE_PASSWORD")
    val canSign = keystorePath != null && !keystorePassword.isNullOrEmpty()
    if (canSign) {
        signingConfigs {
            create("fylvo") {
                storeFile = file(keystorePath!!)
                storePassword = keystorePassword
                keyAlias = "fylvo"
                keyPassword = keystorePassword
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            // Never the debug key: signed with the Fylvo key or not at all.
            if (canSign) signingConfig = signingConfigs.getByName("fylvo")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlin {
        compilerOptions {
            jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
        }
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    testOptions {
        unitTests.isIncludeAndroidResources = true
    }

    packaging {
        resources {
            excludes += "/META-INF/{AL2.0,LGPL2.1}"
        }
    }
}

dependencies {
    val composeBom = platform("androidx.compose:compose-bom:2024.12.01")
    implementation(composeBom)
    androidTestImplementation(composeBom)

    implementation("androidx.core:core-ktx:1.15.0")
    // Per-app language (AppCompatDelegate.setApplicationLocales), see util/AppLanguage.kt.
    implementation("androidx.appcompat:appcompat:1.7.0")
    implementation("androidx.activity:activity-compose:1.9.3")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.8.7")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.8.7")
    implementation("androidx.navigation:navigation-compose:2.8.5")
    implementation("androidx.datastore:datastore-preferences:1.1.1")
    implementation("androidx.documentfile:documentfile:1.0.1")
    implementation("androidx.work:work-runtime-ktx:2.10.0")

    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    debugImplementation("androidx.compose.ui:ui-tooling")

    implementation("com.squareup.retrofit2:retrofit:2.11.0")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("com.jakewharton.retrofit:retrofit2-kotlinx-serialization-converter:1.0.0")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.7.3")

    implementation("io.coil-kt:coil-compose:2.7.0")
    implementation("io.coil-kt:coil-video:2.7.0")

    // In-app video/audio preview. 1.11 needs Kotlin 2.2; stay on 1.10 until the Kotlin plugin moves.
    implementation("androidx.media3:media3-exoplayer:1.10.1")
    implementation("androidx.media3:media3-ui:1.10.1")

    testImplementation("junit:junit:4.13.2")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.9.0")
    testImplementation("org.robolectric:robolectric:4.14.1")

}
