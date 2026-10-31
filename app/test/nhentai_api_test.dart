import 'package:flutter_test/flutter_test.dart';
import 'package:pica_comic/network/nhentai_network/nhentai_api.dart';

void main() {
  final searchResult = {
    "id": 692755,
    "media_id": "4286742",
    "english_title": "English title",
    "japanese_title": "Japanese title",
    "thumbnail": "galleries/4286742/thumb.webp",
    "thumbnail_width": 250,
    "thumbnail_height": 357,
    "num_pages": 205,
    "num_favorites": 0,
    "tag_ids": [17249, 29963, 33173, 23895, 149241],
    "blacklisted": false,
  };

  final gallery = {
    "id": 692755,
    "media_id": "4286742",
    "title": {
      "english": "English title",
      "japanese": "Japanese title",
      "pretty": "Pretty title",
    },
    "cover": {"path": "galleries/4286742/cover.webp.webp"},
    "thumbnail": {"path": "galleries/4286742/thumb.webp"},
    "num_pages": 2,
    "tags": [
      {"id": 29963, "type": "language", "name": "chinese"},
      {"id": 33173, "type": "category", "name": "manga"},
      {"id": 23895, "type": "tag", "name": "yaoi"},
      {"id": 149241, "type": "artist", "name": "oshima kamome"},
      {"id": 1, "type": "parody", "name": "the idolmaster"},
      {"id": 2, "type": "character", "name": "hatsune miku"},
      {"id": 3, "type": "group", "name": "amp"},
    ],
    "pages": [
      {
        "number": 1,
        "path": "galleries/4286742/1.webp",
        "thumbnail": "galleries/4286742/1t.webp",
      },
      {
        "number": 2,
        "path": "galleries/4286742/2.jpg",
        "thumbnail": "galleries/4286742/2t.jpg.webp",
      },
    ],
  };

  test("search result is mapped to a comic brief", () {
    final brief = nhentaiBriefFromApi(searchResult);
    expect(brief.id, "692755");
    expect(brief.title, "English title");
    expect(brief.cover, "https://t.nhentai.net/galleries/4286742/thumb.webp");
    expect(brief.lang, "中文");
    expect(brief.tags, contains("yaoi"));
    // Language tags are handled separately and are not part of the tag map.
    expect(brief.tags, isNot(contains("chinese")));
  });

  test("brief title falls back to japanese title and then id", () {
    final japaneseOnly = Map<String, dynamic>.from(searchResult)
      ..remove("english_title");
    expect(nhentaiBriefFromApi(japaneseOnly).title, "Japanese title");

    final noTitle = Map<String, dynamic>.from(searchResult)
      ..remove("english_title")
      ..remove("japanese_title");
    expect(nhentaiBriefFromApi(noTitle).title, "692755");
  });

  test("language follows the HTML parser precedence", () {
    expect(nhentaiLanguageFromTagIds(["12227"]), "English");
    expect(nhentaiLanguageFromTagIds(["6346"]), "日本語");
    expect(nhentaiLanguageFromTagIds(["29963"]), "中文");
    expect(nhentaiLanguageFromTagIds(["12227", "29963"]), "English");
    expect(nhentaiLanguageFromTagIds([]), "Unknown");
  });

  test("gallery is mapped to a comic", () {
    final comic = nhentaiComicFromApi(gallery);
    expect(comic.id, "692755");
    expect(comic.title, "Pretty title");
    expect(comic.subTitle, "English title");
    expect(comic.cover,
        "https://t.nhentai.net/galleries/4286742/cover.webp.webp");
    expect(comic.tags["Languages"], ["chinese"]);
    expect(comic.tags["Categories"], ["manga"]);
    expect(comic.tags["Tags"], ["yaoi"]);
    expect(comic.tags["Artists"], ["oshima kamome"]);
    expect(comic.tags["Parodies"], ["the idolmaster"]);
    expect(comic.tags["Characters"], ["hatsune miku"]);
    expect(comic.tags["Groups"], ["amp"]);
    expect(comic.tags["Pages"], ["2"]);
    expect(comic.thumbnails, [
      "https://t.nhentai.net/galleries/4286742/1t.webp",
      "https://t.nhentai.net/galleries/4286742/2t.jpg.webp",
    ]);
    expect(comic.favorite, isFalse);
    expect(comic.token, "");
    expect(comic.recommendations, isEmpty);
  });

  test("gallery pages are mapped to image urls", () {
    expect(nhentaiImagesFromApi(gallery), [
      "https://i.nhentai.net/galleries/4286742/1.webp",
      "https://i.nhentai.net/galleries/4286742/2.jpg",
    ]);
  });

  test("category paths are converted to API queries", () {
    expect(nhentaiCategoryQuery("/language/chinese"), "language:chinese");
    expect(nhentaiCategoryQuery("/tag/sole female"), 'tag:"sole female"');
    expect(nhentaiCategoryQuery("/category/manga"), "category:manga");
    expect(nhentaiCategoryQuery(""), "");
    expect(nhentaiCategoryQuery("/broken"), "");
  });

  test("search documents expose results and page count", () {
    final doc = {
      "result": [searchResult],
      "num_pages": 42,
      "per_page": 25,
      "total": 1000,
    };
    expect(nhentaiApiResults(doc), hasLength(1));
    expect(nhentaiApiNumPages(doc), 42);
    expect(nhentaiApiResults(null), isEmpty);
    expect(nhentaiApiNumPages(null), 0);
  });
}
