/// Web implementation: clear Flutter's URL strategy so the engine creates a
/// browser history without any `popstate` listener.
library;

import 'package:flutter_web_plugins/flutter_web_plugins.dart';
import 'package:pica_comic/foundation/log.dart';

void disableFlutterBrowserHistory() {
  try {
    setUrlStrategy(null);
  } catch (e) {
    LogManager.addLog(LogLevel.info, "WebUrlStrategy",
        "could not disable Flutter browser history: $e");
  }
}
