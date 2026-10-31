/// No-op zip implementation for web.
///
/// Creating or extracting zip archives is not supported in the browser build.
/// Methods throw [UnsupportedError] so callers' existing try/catch paths
/// report a normal failure instead of crashing.
library;

class ZipFile {
  ZipFile._(this.path);

  final String path;

  static ZipFile open(String path) => ZipFile._(path);

  static void openAndExtract(String zipFile, String extractTo) {
    throw UnsupportedError('Zip extraction is not supported on web');
  }

  static Future<void> openAndExtractAsync(
      String zipFile, String extractTo) async {
    throw UnsupportedError('Zip extraction is not supported on web');
  }

  void addFile(String name, String sourceFile) {
    throw UnsupportedError('Zip creation is not supported on web');
  }

  void addFileFromBytes(String name, List<int> bytes) {
    throw UnsupportedError('Zip creation is not supported on web');
  }

  void close() {}
}
