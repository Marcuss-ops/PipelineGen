package overlays

// ChapterBulletRole is the wire presentation/editorial role of chapter cards.
// It rides the bullets overlay category like scene_bullet, but with its own
// motion family (text_3d) and font bump, so a chapter opening reads as a
// chapter on screen. The scripts projection sets it; the planner, budgets,
// sections and seal all key off this single constant.
const ChapterBulletRole = "chapter_bullet"

// ChapterTextFontIncreasePX is the chapter-card font bump over the resolved
// phrase size, mirroring PresentationTextFontIncreasePX for date/metric
// cards. RenderingGen fits text shrink-only, so long chapter sentences
// shrink to fit while short ones render bigger — never overflow.
const ChapterTextFontIncreasePX = 24
