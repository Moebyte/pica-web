/// Native stub for [web_runtime.dart]. Every feature is unavailable / no-op.
library;

Future<bool> requestWakeLock() async => false;

Future<void> releaseWakeLock() async {}

bool get isWebFullscreen => false;

Future<bool> enterFullscreen() async => false;

Future<void> exitFullscreen() async {}

bool get isMobileBrowser => false;

void unlockScreenOrientation() {}

void addPageLifecycleListener(void Function(bool hidden) listener) {}

void removePageLifecycleListener(void Function(bool hidden) listener) {}
