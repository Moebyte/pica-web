/// Native stub: platform back buttons are handled by the OS / Flutter.
library;

class WebHistoryBridge {
  static void install() {}

  /// Called when the app navigates to a new page; re-arms the sentinel if a
  /// previous back press at the root consumed it.
  static void ensureSentinel() {}
}
