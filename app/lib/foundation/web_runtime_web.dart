/// Browser implementation of the small runtime helpers in web_runtime.dart.
library;

import 'dart:async';
import 'dart:js_interop';

import 'package:pica_comic/foundation/log.dart';
import 'package:web/web.dart' as web;

bool _lifecycleInstalled = false;
bool _wakeLockWanted = false;
bool _wakeLockRequesting = false;
bool _fullscreenOwned = false;
bool _fullscreenListenerInstalled = false;

final List<void Function(bool hidden)> _lifecycleListeners = [];

/// Screen Wake Lock API. The browser automatically releases the lock when the
/// page is hidden, so it must be re-acquired on `visibilitychange`.
Future<bool> requestWakeLock() async {
  _wakeLockWanted = true;
  return _acquireWakeLock();
}

Future<void> releaseWakeLock() async {
  _wakeLockWanted = false;
  await _releaseWakeLockSentinel();
}

Future<bool> _acquireWakeLock() async {
  if (!_wakeLockWanted || _wakeLockRequesting || _wakeLockSentinel != null) {
    return _wakeLockSentinel != null;
  }
  _wakeLockRequesting = true;
  try {
    // `navigator.wakeLock` is missing on unsupported browsers (and in some
    // headless configurations); the access itself may throw.
    final sentinel = await web.window.navigator.wakeLock.request('screen').toDart;
    _wakeLockSentinel = sentinel;
    try {
      sentinel.addEventListener(
        'release',
        ((web.Event event) {
          if (identical(_wakeLockSentinel, sentinel)) {
            _wakeLockSentinel = null;
          }
        }).toJS,
      );
    } catch (_) {}
    return true;
  } catch (e) {
    // Not supported / permission denied: silently ignore, reading still works.
    LogManager.addLog(LogLevel.info, "WebRuntime",
        "wake lock request failed: $e");
    return false;
  } finally {
    _wakeLockRequesting = false;
  }
}

WebWakeLockSentinel? _wakeLockSentinel;

typedef WebWakeLockSentinel = web.WakeLockSentinel;

Future<void> _releaseWakeLockSentinel() async {
  final sentinel = _wakeLockSentinel;
  _wakeLockSentinel = null;
  if (sentinel == null) return;
  try {
    await sentinel.release().toDart;
  } catch (_) {
    // Already released by the browser.
  }
}

void _installLifecycleListeners() {
  if (_lifecycleInstalled) return;
  _lifecycleInstalled = true;
  try {
    web.document.addEventListener(
      'visibilitychange',
      ((web.Event event) {
        final hidden = web.document.visibilityState == 'hidden';
        if (hidden) {
          _releaseWakeLockSentinel();
        } else if (_wakeLockWanted) {
          _acquireWakeLock();
        }
        _notifyLifecycle(hidden);
      }).toJS,
    );
    web.window.addEventListener(
      'pagehide',
      ((web.Event event) {
        _releaseWakeLockSentinel();
        _notifyLifecycle(true);
      }).toJS,
    );
  } catch (e) {
    LogManager.addLog(
        LogLevel.info, "WebRuntime", "install lifecycle listeners failed: $e");
  }
}

void _notifyLifecycle(bool hidden) {
  for (final listener in List.of(_lifecycleListeners)) {
    try {
      listener(hidden);
    } catch (e, s) {
      LogManager.addLog(LogLevel.error, "WebRuntime",
          "page lifecycle listener failed: $e\n$s");
    }
  }
}

void addPageLifecycleListener(void Function(bool hidden) listener) {
  _installLifecycleListeners();
  if (!_lifecycleListeners.contains(listener)) {
    _lifecycleListeners.add(listener);
  }
}

void removePageLifecycleListener(void Function(bool hidden) listener) {
  _lifecycleListeners.remove(listener);
}

bool get isWebFullscreen {
  try {
    return web.document.fullscreenElement != null;
  } catch (_) {
    return false;
  }
}

/// Must be called from a user activation context (tap / key event). If the
/// browser refuses, returns false and the caller can offer a manual button.
Future<bool> enterFullscreen() async {
  try {
    _installFullscreenListener();
    if (web.document.fullscreenElement == null) {
      final element = web.document.documentElement;
      if (element == null) return false;
      await element.requestFullscreen().toDart;
    }
    _fullscreenOwned = web.document.fullscreenElement != null;
    return _fullscreenOwned;
  } catch (e) {
    LogManager.addLog(
        LogLevel.info, "WebRuntime", "requestFullscreen failed: $e");
    return false;
  }
}

Future<void> exitFullscreen() async {
  try {
    if (_fullscreenOwned && web.document.fullscreenElement != null) {
      await web.document.exitFullscreen().toDart;
    }
  } catch (_) {
    // The browser may already have left fullscreen (Esc / gesture).
  } finally {
    _fullscreenOwned = false;
  }
}

void _installFullscreenListener() {
  if (_fullscreenListenerInstalled) return;
  _fullscreenListenerInstalled = true;
  try {
    web.document.addEventListener(
      'fullscreenchange',
      ((web.Event event) {
        if (web.document.fullscreenElement == null) {
          _fullscreenOwned = false;
        }
      }).toJS,
    );
  } catch (_) {}
}

/// Coarse mobile browser detection: Android/iOS user agent or a touch-first
/// pointer. Used to auto-enter fullscreen only on phones.
bool get isMobileBrowser {
  try {
    final ua = web.window.navigator.userAgent;
    if (RegExp(r'Android|iPhone|iPad|iPod|Mobile', caseSensitive: false)
        .hasMatch(ua)) {
      return true;
    }
    return web.window.matchMedia('(pointer: coarse)').matches;
  } catch (_) {
    return false;
  }
}
