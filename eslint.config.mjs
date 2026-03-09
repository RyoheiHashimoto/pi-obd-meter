export default [
  {
    files: ["gas/**/*.gs", "gas/**/*.js"],
    languageOptions: {
      ecmaVersion: 2020,
      sourceType: "script",
      globals: {
        SpreadsheetApp: "readonly",
        ContentService: "readonly",
        HtmlService: "readonly",
        PropertiesService: "readonly",
        Logger: "readonly",
        Utilities: "readonly",
        UrlFetchApp: "readonly",
        Session: "readonly",
        LockService: "readonly",
        CacheService: "readonly",
        console: "readonly",
      },
    },
    rules: {
      "no-undef": "error",
      "no-unused-vars": ["warn", { args: "none" }],
      "no-redeclare": "error",
    },
  },
];
