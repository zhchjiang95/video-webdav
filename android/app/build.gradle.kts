plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

val appVersionName = System.getenv("APP_VERSION_NAME")?.takeIf { it.isNotBlank() } ?: "1.0.0"
val appVersionCode = System.getenv("APP_VERSION_CODE")?.toIntOrNull() ?: 1

android {
    namespace = "com.videoserver.webdav"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.videoserver.webdav"
        minSdk = 26
        targetSdk = 35
        versionCode = appVersionCode
        versionName = appVersionName
    }

    // 支持自定义签名密钥；未配置环境变量时回退到 debug 签名，保证云端构建的 Release APK 可直接安装
    signingConfigs {
        val ksPath = System.getenv("ANDROID_KEYSTORE_PATH")
        if (!ksPath.isNullOrBlank() && file(ksPath).exists()) {
            create("release") {
                storeFile = file(ksPath)
                storePassword = System.getenv("ANDROID_KEYSTORE_PASSWORD") ?: ""
                keyAlias = System.getenv("ANDROID_KEY_ALIAS") ?: ""
                keyPassword = System.getenv("ANDROID_KEY_PASSWORD") ?: ""
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
            signingConfig = signingConfigs.findByName("release")
                ?: signingConfigs.getByName("debug")
        }
        debug {
            applicationIdSuffix = ".debug"
        }
    }

    // 按 ABI 分包输出体积更小的 arm64-v8a / armeabi-v7a APK，同时保留 universal 通用包
    splits {
        abi {
            isEnable = true
            reset()
            include("arm64-v8a", "armeabi-v7a")
            isUniversalApk = true
        }
    }

    // 必须开启 legacy packaging，使 Android 将 jniLibs 中的 libvideoserver.so 解压到 nativeLibraryDir 以支持执行
    packaging {
        jniLibs {
            useLegacyPackaging = true
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }
}

dependencies {
    implementation("androidx.core:core-ktx:1.15.0")
    implementation("androidx.appcompat:appcompat:1.7.0")
    implementation("com.google.android.material:material:1.12.0")
    implementation("androidx.activity:activity-ktx:1.9.3")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.7")
    implementation("androidx.constraintlayout:constraintlayout:2.2.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.9.0")
}
