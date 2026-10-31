import 'package:flutter/services.dart';
import 'package:pica_comic/foundation/app.dart';
import 'package:pica_comic/foundation/web_runtime.dart';

/// Keeps the screen awake while reading.
///
/// On the web this uses the Screen Wake Lock API (`navigator.wakeLock`); on
/// Android/iOS it delegates to the existing platform channel. All failures are
/// silently ignored, since keeping the screen on is a convenience only.
void setKeepScreenOn() async {
  if (App.isWeb) {
    await requestWakeLock();
    return;
  }
  if (!App.isMobile) return;
  try {
    var channel = const MethodChannel("com.github.pacalini.pica_comic/keepScreenOn");
    await channel.invokeMethod("set");
  } catch (_) {
    // Platform channel unavailable (e.g. unsupported build).
  }
}

void cancelKeepScreenOn() async {
  if (App.isWeb) {
    await releaseWakeLock();
    return;
  }
  if (!App.isMobile) return;
  try {
    var channel = const MethodChannel("com.github.pacalini.pica_comic/keepScreenOn");
    await channel.invokeMethod("cancel");
  } catch (_) {
    // Platform channel unavailable.
  }
}
