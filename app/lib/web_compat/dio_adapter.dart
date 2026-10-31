/// Platform-specific Dio adapter factory.
///
/// Native: `IOHttpClientAdapter` / HTTP/2 adapter.
/// Web: `BrowserHttpClientAdapter` (XMLHttpRequest/fetch).
library;

export 'dio_adapter_io.dart'
    if (dart.library.js_interop) 'dio_adapter_web.dart';
