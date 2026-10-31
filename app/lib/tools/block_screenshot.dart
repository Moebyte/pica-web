import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

void blockScreenshot(){
  if (kIsWeb) {
    // Not supported in browsers.
    return;
  }
  const MethodChannel("com.github.pacalini.pica_comic/screenshot").invokeMethod("blockScreenshot");
}