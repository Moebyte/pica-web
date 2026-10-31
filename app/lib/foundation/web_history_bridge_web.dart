/// Browser implementation of [WebHistoryBridge].
library;

import 'dart:js_interop';

import 'package:pica_comic/foundation/app.dart';
import 'package:pica_comic/foundation/log.dart';
import 'package:web/web.dart' as web;

class WebHistoryBridge {
  static const int _debounceMs = 150;

  static bool _installed = false;
  static bool _sentinelArmed = false;
  static int _lastHandledMs = 0;
  static bool _handling = false;

  /// Installs the sentinel entry and the `popstate` listener. Must be called
  /// after `runApp` (the navigator keys are only useful once the first frame
  /// has been scheduled).
  static void install() {
    if (_installed) return;
    try {
      final href = web.window.location.href;
      // Replace the entry the app was loaded into, then add the sentinel on
      // top. In-app navigation never touches browser history, so exactly one
      // browser back is available for "go back one app page".
      web.window.history.replaceState(null, '', href);
      _pushSentinel();
      _sentinelArmed = true;
      web.window.addEventListener(
        'popstate',
        ((web.Event event) => _onPopState()).toJS,
      );
      _installed = true;
      LogManager.addLog(
          LogLevel.info, "WebHistoryBridge", "history bridge installed");
    } catch (e, s) {
      LogManager.addLog(LogLevel.error, "WebHistoryBridge",
          "install failed: $e\n$s");
    }
  }

  static void _pushSentinel() {
    try {
      web.window.history.pushState(null, '', web.window.location.href);
      _sentinelArmed = true;
    } catch (e, s) {
      LogManager.addLog(LogLevel.error, "WebHistoryBridge",
          "pushState sentinel failed: $e\n$s");
    }
  }

  /// Re-arms the sentinel after a root-level back consumed it. Called whenever
  /// the app opens a new page, so later back presses keep working.
  static void ensureSentinel() {
    if (!_installed || _sentinelArmed) return;
    try {
      _pushSentinel();
    } catch (e, s) {
      LogManager.addLog(LogLevel.error, "WebHistoryBridge",
          "ensure sentinel failed: $e\n$s");
    }
  }

  static void _onPopState() {
    final now = DateTime.now().millisecondsSinceEpoch;
    if (_handling || now - _lastHandledMs < _debounceMs) {
      return;
    }
    _lastHandledMs = now;
    _handling = true;
    try {
      bool popped = false;
      // The root navigator owns dialogs and full-screen pages (reader).
      final root = App.navigatorKey.currentState;
      if (root != null && root.canPop()) {
        root.pop();
        popped = true;
      } else {
        // Some pages (detail/search) live in MainPage's nested navigator.
        final nested = App.mainNavigatorKey?.currentState;
        if (nested != null && nested.canPop()) {
          nested.pop();
          popped = true;
        }
      }
      if (popped) {
        // Browser history was one entry shallower after `popstate`; restore
        // the sentinel so the next back can be intercepted again.
        _pushSentinel();
      } else {
        // We are back on the app's origin entry. Leave it disarmed so the next
        // back can leave the app; any later in-app navigation re-arms it via
        // [ensureSentinel].
        _sentinelArmed = false;
      }
    } catch (e, s) {
      LogManager.addLog(LogLevel.error, "WebHistoryBridge",
          "popstate handling failed: $e\n$s");
    } finally {
      _handling = false;
    }
  }
}
