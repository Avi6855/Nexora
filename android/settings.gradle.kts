pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
        maven {
            url = uri("https://mirrors.cloud.tencent.com/nexus/repository/maven-public/")
        }
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
        maven {
            url = uri("https://mirrors.cloud.tencent.com/nexus/repository/maven-public/")
        }
    }
}

rootProject.name = "Nexora"
include(":app")
