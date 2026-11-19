# Keep kotlinx.serialization generated serializers
-keepclassmembers class io.github.svenbuild.fylvo.android.api.** {
    *** Companion;
}
-keepclasseswithmembers class io.github.svenbuild.fylvo.android.api.** {
    kotlinx.serialization.KSerializer serializer*(...);
}
-keepattributes *Annotation*, InnerClasses
-dontnote kotlinx.serialization.**
-dontwarn okhttp3.**
-dontwarn okio.**
