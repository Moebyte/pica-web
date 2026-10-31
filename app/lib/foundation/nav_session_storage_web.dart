/// Browser sessionStorage implementation.
library;

import 'package:web/web.dart' as web;

String? readSessionValue(String key) {
  try {
    return web.window.sessionStorage.getItem(key);
  } catch (_) {
    return null;
  }
}

void writeSessionValue(String key, String value) {
  try {
    web.window.sessionStorage.setItem(key, value);
  } catch (_) {
    // Quota exceeded / storage disabled: snapshots are best effort only.
  }
}

void removeSessionValue(String key) {
  try {
    web.window.sessionStorage.removeItem(key);
  } catch (_) {}
}
