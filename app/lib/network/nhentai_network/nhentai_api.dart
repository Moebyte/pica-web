/// Mapping helpers for the nhentai API v2.
///
/// The HTML pages (`/`, `/search`, `/g/<id>/`) are protected by a Cloudflare
/// challenge in the web build, while `/api/v2/*` is reachable through the
/// gateway. These pure functions convert API documents into the models used by
/// the rest of the app, so they can be unit tested on the Dart VM.
library;

import 'models.dart';
import 'tags.dart';

const String nhentaiApiBase = "https://nhentai.net/api/v2";
const String nhentaiImageBase = "https://i.nhentai.net";
const String nhentaiThumbnailBase = "https://t.nhentai.net";

/// The `result` array of a search/galleries API document as maps.
List<Map<String, dynamic>> nhentaiApiResults(dynamic json) {
  if (json is! Map) {
    return const [];
  }
  final result = json["result"];
  if (result is! List) {
    return const [];
  }
  return [
    for (final item in result)
      if (item is Map) item.cast<String, dynamic>()
  ];
}

/// Total number of pages of a search API document (`num_pages`).
int nhentaiApiNumPages(dynamic json) {
  if (json is! Map) {
    return 0;
  }
  final value = json["num_pages"];
  return value is num ? value.toInt() : 0;
}

/// Converts an entry of `result` (search / galleries) to a comic brief.
NhentaiComicBrief nhentaiBriefFromApi(Map<String, dynamic> json) {
  final id = json["id"]?.toString() ?? "";
  final title = _firstNonEmpty([json["english_title"], json["japanese_title"]]) ?? id;
  final thumbnail = _nonEmptyString(json["thumbnail"]);
  final tagIds = nhentaiTagIdsFromApi(json);
  return NhentaiComicBrief(
    title,
    thumbnail == null ? "" : "$nhentaiThumbnailBase/$thumbnail",
    id,
    nhentaiLanguageFromTagIds(tagIds),
    [
      for (final tagId in tagIds)
        if (nhentaiTags[tagId] != null) nhentaiTags[tagId]!
    ],
  );
}

/// `tag_ids` of a search result, as strings (keys of [nhentaiTags]).
List<String> nhentaiTagIdsFromApi(Map<String, dynamic> json) {
  final ids = json["tag_ids"];
  if (ids is! List) {
    return const [];
  }
  return [
    for (final id in ids)
      if (id != null) id.toString()
  ];
}

/// Infers the language of a gallery from its tag ids, following the same
/// precedence as the HTML parser.
String nhentaiLanguageFromTagIds(Iterable<String> tagIds) {
  final ids = tagIds.toSet();
  if (ids.contains("12227")) {
    return "English";
  } else if (ids.contains("6346")) {
    return "日本語";
  } else if (ids.contains("29963")) {
    return "中文";
  }
  return "Unknown";
}

/// Converts a `/api/v2/galleries/<id>` document to a comic.
NhentaiComic nhentaiComicFromApi(Map<String, dynamic> json) {
  final id = json["id"]?.toString() ?? "";

  final titleMap = json["title"] is Map
      ? (json["title"] as Map).cast<String, dynamic>()
      : const <String, dynamic>{};
  final english = _nonEmptyString(titleMap["english"]);
  final japanese = _nonEmptyString(titleMap["japanese"]);
  final pretty = _nonEmptyString(titleMap["pretty"]);
  final title = pretty ?? english ?? japanese ?? id;
  final subTitle = english != null && english != title
      ? english
      : (japanese != null && japanese != title ? japanese : "");

  final coverMap = json["cover"] is Map
      ? (json["cover"] as Map).cast<String, dynamic>()
      : const <String, dynamic>{};
  final coverPath = _nonEmptyString(coverMap["path"]);

  final tags = <String, List<String>>{};
  final rawTags = json["tags"];
  if (rawTags is List) {
    for (final rawTag in rawTags) {
      if (rawTag is! Map) {
        continue;
      }
      final type = _nonEmptyString(rawTag["type"]);
      final name = _nonEmptyString(rawTag["name"]);
      if (type == null || name == null) {
        continue;
      }
      tags.putIfAbsent(nhentaiTagGroupName(type), () => []).add(name);
    }
  }

  final numPages = json["num_pages"];
  if (numPages is num) {
    tags["Pages"] = [numPages.toInt().toString()];
  }

  final thumbnails = <String>[];
  final pages = json["pages"];
  if (pages is List) {
    for (final page in pages) {
      if (page is! Map) {
        continue;
      }
      final thumbnail = _nonEmptyString(page["thumbnail"]);
      if (thumbnail != null) {
        thumbnails.add("$nhentaiThumbnailBase/$thumbnail");
      }
    }
  }

  return NhentaiComic(
    id,
    title,
    subTitle,
    coverPath == null ? "" : "$nhentaiThumbnailBase/$coverPath",
    tags,
    false,
    thumbnails,
    const [],
    "",
  );
}

/// Image URLs of a `/api/v2/galleries/<id>` document, in page order.
List<String> nhentaiImagesFromApi(Map<String, dynamic> json) {
  final pages = json["pages"];
  if (pages is! List) {
    return const [];
  }
  return [
    for (final page in pages)
      if (page is Map && _nonEmptyString(page["path"]) != null)
        "$nhentaiImageBase/${page["path"]}"
  ];
}

/// Converts an HTML-style category path (`/language/chinese`,
/// `/tag/sole female`, ...) to an API search query (`language:chinese`,
/// `tag:"sole female"`, ...).
String nhentaiCategoryQuery(String path) {
  var parts = path.split('/').where((part) => part.isNotEmpty).toList();
  if (parts.length < 2) {
    return "";
  }
  var type = parts[0];
  var name = parts.sublist(1).join('/');
  if (name.contains(' ')) {
    return '$type:"${name.replaceAll('"', '')}"';
  }
  return '$type:$name';
}

/// Maps an API tag `type` to the group names used by the HTML parser / UI.
String nhentaiTagGroupName(String type) => switch (type) {
      "parody" => "Parodies",
      "character" => "Characters",
      "tag" => "Tags",
      "artist" => "Artists",
      "group" => "Groups",
      "language" => "Languages",
      "category" => "Categories",
      _ => type,
    };

String? _firstNonEmpty(List<dynamic> values) {
  for (final value in values) {
    final text = _nonEmptyString(value);
    if (text != null) {
      return text;
    }
  }
  return null;
}

String? _nonEmptyString(dynamic value) {
  if (value is String && value.trim().isNotEmpty) {
    return value;
  }
  return null;
}
