package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type providedSoundEffect struct {
	driveID  string
	filename string
	name     string
	family   string
	subtype  string
	mood     string
	energy   string
	bestFor  []string
	tags     []string
}

var providedSoundEffects = []providedSoundEffect{
	{"1J6oTa66IfB3k8Pt0IUQuSIEHgXa9SctD", "sfx_ui_discord_join_02.mp3", "Discord Join", "ui", "notification_click", "clean", "low", []string{"notification", "join", "micro_accent"}, []string{"discord", "join", "notification", "ui", "chime"}},
	{"13H2YKkKSCMlenuyGVFHCfYPZ_k946rk9", "sfx_impact_bonk_comedy_01.mp3", "Bonk", "impact", "comic_impact", "comedic", "medium", []string{"comedy", "reaction", "dramatic_hit"}, []string{"bonk", "impact", "comedy", "reaction", "meme"}},
	{"1lof30_6JSMwHtqNGyh9X4dmxOnfkwcmj", "sfx_ui_discord_notification_02.mp3", "Discord Notification", "ui", "notification_click", "clean", "low", []string{"notification", "label", "micro_accent"}, []string{"discord", "notification", "ui", "alert", "chime"}},
	{"1ZFy9nRD3zEaDBGKudXGqLTX3LWmvwdPB", "sfx_cartoon_wet_fart_01.mp3", "Wet Fart Meme", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"wet_fart", "fart", "cartoon", "meme", "reaction"}},
	{"1fgqaXiK578LxgpiLQUQ7ESb1LKQgalIy", "sfx_cartoon_yoshi_mlem_01.mp3", "Yoshi Mlem", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"yoshi", "mlem", "cartoon", "meme", "reaction"}},
	{"19O8CHXpEsDaqiuwzUf3W-SVMp8KomLYA", "sfx_foley_minecraft_door_01.mp3", "Minecraft Door", "foley", "door_mechanical", "neutral", "medium", []string{"action_match", "handling", "transition"}, []string{"minecraft", "door", "open", "close", "foley", "game"}},
	{"1h6iwmOZF28RX6aBFlVFZodRcbN4XNOlT", "sfx_gaming_mario_coin_01.mp3", "Mario Coin", "gaming", "game_ui", "playful", "low", []string{"reward", "counter", "success"}, []string{"mario", "coin", "gaming", "reward", "ui"}},
	{"1ym8XD0r09-SIzClAuMoKt0cOwt8Sa8DT", "sfx_cartoon_fart_02.mp3", "Kentut Fart", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"fart", "kentut", "cartoon", "meme", "reaction"}},
	{"1qn83GFWnHZN7bllw6RH5YKvlEJBgGfF9", "sfx_foley_minecraft_eating_01.mp3", "Minecraft Eating", "foley", "eating_mouth", "comedic", "medium", []string{"action_match", "comedy", "texture"}, []string{"minecraft", "eating", "mouth", "foley", "game"}},
	{"1L_UX2FIHYhjrpeYmPhc0gvelHGD6oqN-", "sfx_cartoon_bruh_reaction_01.mp3", "Bruh Reaction", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"bruh", "vocal", "huh", "ah", "meme", "confused", "reaction"}},
	{"1gt0YmLQa9uqXeHDbTkbpn_005R_wLef3", "sfx_impact_punch_comedy_01.mp3", "Punch", "impact", "hit_punch", "comedic", "high", []string{"comedy", "dramatic_hit", "reaction"}, []string{"punch", "slap", "impact", "hit", "combat", "gaming"}},
	{"1AGKnpKAagHcl3GKiRzjw7n4zNLY0PYF0", "sfx_cartoon_nope_reaction_01.mp3", "Nope Reaction", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"nope", "vocal", "tf2", "engineer", "meme", "comedy"}},
	{"1op6uZzu29-poOM53bGHq1KteDzR4U474", "sfx_impact_minecraft_fall_damage_01.mp3", "Minecraft Fall Damage", "impact", "bone_break", "dramatic", "high", []string{"dramatic_hit", "damage", "comedy"}, []string{"minecraft", "fall", "damage", "crack", "bone", "gaming"}},
	{"1A45OTTmz0SrN8UP58ONzxGMrzWjuFUJd", "sfx_foley_camera_shutter_03.mp3", "Camera Shutter", "foley", "camera_shutter", "documentary", "low", []string{"action_match", "camera", "transition"}, []string{"camera", "shutter", "click", "photo", "mechanical", "foley"}},
	{"127ZLnNn-4iL0TcDtjOVOWefJASUoqXfY", "sfx_transition_whoosh_fast_02.mp3", "Fast Whoosh", "transition", "fast_swipe", "action", "high", []string{"cut", "reveal", "motion"}, []string{"whoosh", "whip", "swish", "transition", "fast"}},
	{"1lzhdsxu8-4bfBbIpquPfnrMouZqOY6Mw", "sfx_ui_iphone_notification_02.mp3", "iPhone Notification", "ui", "notification_click", "clean", "low", []string{"notification", "label", "micro_accent"}, []string{"iphone", "notification", "ping", "alert", "digital", "ui"}},
	{"1lWhiNP99YFwt3lwWbJsmvLOW8vMReOVj", "sfx_ui_awkward_magical_chime_01.mp3", "Awkward Moment Chime", "ui", "magical_chime", "playful", "low", []string{"label", "reveal", "notification"}, []string{"awkward", "chime", "sparkle", "magical", "glissando", "anime"}},
	{"1g4Et-tG7ukJtS90qNtQB2DN8f1OcHqt3", "sfx_gaming_damage_grunt_01.mp3", "Gaming Damage Grunt", "gaming", "damage_grunt", "dramatic", "medium", []string{"damage", "reaction", "gaming"}, []string{"uh", "oof", "vocal", "grunt", "gaming", "meme"}},
	{"1LOUvET_UaWtfNz-LqTAa3wWvHEmhri5I", "sfx_cartoon_boy_what_the_hell_01.mp3", "Boy What the Hell Reaction", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"boy", "what", "hell", "vocal", "talking_tom", "confused", "meme"}},
	{"1I-mej2S3Gn-zDD87WEVhaJp8wx5DmLzL", "sfx_cartoon_taco_bell_meme_01.mp3", "Taco Bell Meme", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"taco_bell", "meme", "comedy", "cartoon"}},
	{"1XQmly1KduYaLIKzZqO5xurkItN51BGTH", "sfx_cartoon_what_the_dog_doing_01.mp3", "What the Dog Doing", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"dog", "vocal", "meme", "comedy", "reaction"}},
	{"1AiyK4nWqyntXwleOgvd6Ukr_WGzNWzQV", "sfx_cartoon_spongebob_sad_face_01.mp3", "SpongeBob Sad Face", "cartoon", "comic_reaction", "sad", "medium", []string{"comedy", "reaction", "failure"}, []string{"spongebob", "sad", "face", "reaction", "meme"}},
	{"1a_LQ303GRLYOFkZCCgFc9QeKDqXd9NME", "sfx_cartoon_spongebob_disappointed_01.mp3", "SpongeBob Disappointed", "cartoon", "comic_reaction", "disappointed", "medium", []string{"comedy", "reaction", "failure"}, []string{"spongebob", "disappointed", "reaction", "meme"}},
	{"189inEAV2IP09IayGU7Q7eon9zKu3e3oG", "sfx_cartoon_japan_oppai_meme_01.mp3", "Japan Oppai Meme", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"japan", "oppai", "vocal", "meme", "reaction"}},
	{"14qUa3YRNkfSQjnxPvWTYxms9zLyYHhGJ", "sfx_cartoon_raul_vocal_meme_01.mp3", "Raul Vocal Meme", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"raul", "vocal", "voice", "meme", "name"}},
	{"1tSqcU8U702f0jZ1vGFOcLpxWkEh-41oI", "sfx_foley_munch_eating_01.mp3", "Munch Eating", "foley", "eating_mouth", "comedic", "medium", []string{"action_match", "comedy", "texture"}, []string{"munch", "eating", "mouth", "foley"}},
	{"10aNegNKkJbOkDuh-AYp0GkZH2ZmjNY9p", "sfx_foley_throwing_action_01.mp3", "Throwing Action", "foley", "action_throw", "action", "medium", []string{"action_match", "motion", "impact"}, []string{"throwing", "action", "motion", "foley"}},
	{"1bBenasDZBPOKU2bw8awo5ZVbgE03meQE", "sfx_ambient_angels_singing_01.mp3", "Angels Singing", "ambient", "heavenly_choir", "majestic", "medium", []string{"reveal", "wonder", "dramatic"}, []string{"angels", "singing", "choir", "heavenly", "atmosphere"}},
	{"1fJWKIpYCbXxoKdhlfIJO3ZoYIdDzP_IK", "sfx_cartoon_herta_kuru_kuru_01.mp3", "Herta Kuru Kuru", "cartoon", "comic_reaction", "playful", "medium", []string{"comedy", "reaction", "meme"}, []string{"herta", "honkai", "kuru_kuru", "vocal", "meme"}},
	{"1E3YtrzswA7RBA-0PX7wYPUHzOiu3kLNk", "sfx_transition_dragon_ball_teleport_01.mp3", "Dragon Ball Teleport", "transition", "teleport_warp", "action", "high", []string{"reveal", "motion", "rank_change"}, []string{"dragon_ball", "teleport", "anime", "warp", "transition"}},
	{"1x6SQUCECuMlRHedNaOV-97Kh9jhUiSYY", "sfx_cartoon_bad_to_the_bone_01.mp3", "Bad to the Bone Meme", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"bad_to_the_bone", "meme", "vocal", "comedy"}},
	{"1XOVWjI27FtjB36GH-BUYkwavVva7Abvc", "sfx_cartoon_brook_laugh_one_piece_01.mp3", "Brook Laugh", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"one_piece", "brook", "laugh", "vocal", "meme"}},
	{"13_g2gCafjh8CKIUz3e48rdF7bVvdZDDw", "sfx_cartoon_bad_to_the_bone_02.mp3", "Bad to the Bone Meme 2", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"bad_to_the_bone", "meme", "vocal", "comedy"}},
	{"1YCD_tbUxMINbzXT7bGWV6QkvkZiHwzLJ", "sfx_cartoon_ceeday_huh_01.mp3", "Ceeday Huh", "cartoon", "comic_reaction", "confused", "medium", []string{"comedy", "reaction", "meme"}, []string{"ceeday", "huh", "vocal", "confused", "meme"}},
	{"1eOrp-NouQXHAsjSbYsFyYPkLS65Lpat7", "sfx_cartoon_rizz_meme_01.mp3", "Rizz Meme", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"rizz", "vocal", "meme", "comedy"}},
	{"1RARWYsE5PWaESE4cR3iSPuYhaOPhbLcM", "sfx_cartoon_fart_reverb_01.mp3", "Fart with Reverb", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"fart", "reverb", "cartoon", "meme", "reaction"}},
	{"1cXl8aIjUpnoBUFyC-yxWJvGhfhPUPCIg", "sfx_impact_emotional_damage_01.mp3", "Emotional Damage", "impact", "cinematic_boom", "dramatic", "high", []string{"dramatic_hit", "failure", "reaction"}, []string{"emotional_damage", "meme", "impact", "cinematic", "bass"}},
	{"1LoxoZERzvVKHDIcUi-ZkSPp5VvNlCU6d", "sfx_foley_snore_mimimimimi_01.mp3", "Snore Mimimimimi", "foley", "snore_sleep", "comedic", "low", []string{"action_match", "comedy", "reaction"}, []string{"snore", "sleep", "mimimimimi", "foley", "comedy"}},
	{"17LXfTp8sUhj-EWwXZMd1Y_ONJCh6NmpM", "sfx_cartoon_yowai_mo_01.mp3", "Yowai Mo", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"yowai_mo", "anime", "vocal", "meme"}},
	{"1nNrW6FoGWlx9WEGEUmb-qfUW5iRjr1vf", "sfx_ui_one_piece_ringtone_01.mp3", "One Piece Ringtone", "ui", "ringtone", "playful", "low", []string{"notification", "label", "micro_accent"}, []string{"one_piece", "ringtone", "notification", "ui"}},
	{"1m46wAERhQJGAO-uFVwfjPmtl5XZJhcYQ", "sfx_cartoon_wtf_reaction_01.mp3", "WTF Reaction", "cartoon", "comic_reaction", "confused", "medium", []string{"comedy", "reaction", "meme"}, []string{"wtf", "reaction", "vocal", "meme"}},
	{"1QG5Q3ID-09nsFisyB-32HYA4JeRZm1vg", "sfx_cartoon_maju3_meme_01.mp3", "Maju3 Meme", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"maju3", "vocal", "meme", "reaction"}},
	{"1Zj7ZeY7fw_1NOPMLTcOShAXm6tgZJPEe", "sfx_transition_one_piece_enemy_intro_01.mp3", "One Piece Enemy Introduction", "transition", "anime_reveal", "dramatic", "high", []string{"reveal", "rank_change", "dramatic_hit"}, []string{"one_piece", "enemy", "introduction", "anime", "reveal"}},
	{"1ipHbAhRaE_S9POzQTyp_XK9OZvHPNr6y", "sfx_transition_one_piece_flashback_01.mp3", "One Piece Flashback", "transition", "flashback", "dramatic", "medium", []string{"reveal", "cut", "montage"}, []string{"one_piece", "flashback", "anime", "transition"}},
	{"1lf5Of_p1QIJ7MAiwh90bfYEcJM4sgr_d", "sfx_cartoon_don_one_piece_01.mp3", "Don One Piece", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"one_piece", "don", "vocal", "meme"}},
	{"1qUVHqL0KsXm1NHdoYMwXxX5cOKtVV1nz", "sfx_transition_one_piece_flying_fast_01.mp3", "One Piece Flying Fast", "transition", "fast_swipe", "action", "high", []string{"cut", "reveal", "motion"}, []string{"one_piece", "flying", "speed", "whoosh", "transition"}},
	{"1-4iIrXnkc9Ck5Auwzq92ufK-B3IbCHYe", "sfx_foley_one_piece_gulp_01.mp3", "One Piece Gulp", "foley", "gulp_swallow", "comedic", "low", []string{"action_match", "comedy", "texture"}, []string{"one_piece", "gulp", "swallow", "foley"}},
	{"1XOQrfOrnS45ryjKtEIN-DgaRQz3__xG7", "sfx_cartoon_kerja_bagus_01.mp3", "Kerja Bagus Meme", "cartoon", "comic_reaction", "playful", "medium", []string{"comedy", "reaction", "meme"}, []string{"kerja_bagus", "good_job", "vocal", "meme"}},
	{"1OMhFw9VCJLPWwLaUmAhogrA5QWrxD6Ap", "sfx_cartoon_shocked_reaction_01.mp3", "Shocked Reaction", "cartoon", "comic_reaction", "surprised", "medium", []string{"comedy", "reaction", "reveal"}, []string{"shocked", "surprise", "reaction", "meme"}},
	{"1sALcq2Ov11HomF4dMd4CW08tFe6gKRXu", "sfx_misc_1_108_01.mp3", "1-108", "misc", "meme_accent", "neutral", "medium", []string{"accent", "reaction", "meme"}, []string{"1-108", "meme", "sound_effect"}},
	{"1i8YUAamiPTmRAZAG83FZcbKY14AGFvJd", "sfx_cartoon_laughter_short_01.mp3", "Laughter Short", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"laughter", "laugh", "vocal", "comedy"}},
	{"1Yvpp0x_hBWzs05OTiTvMRB3swu0ehGYD", "sfx_ambient_eagle_rahhh_01.mp3", "Eagle Rahhh", "ambient", "animal_call", "dramatic", "medium", []string{"reveal", "reaction", "atmosphere"}, []string{"eagle", "hawk", "bird", "screech", "nature"}},
	{"1AVIAd8Ut4VwnWconLNdkPD4i7n9X8-LA", "sfx_cartoon_oh_shit_bass_01.mp3", "Oh Shit Bass Reaction", "cartoon", "comic_reaction", "shocked", "high", []string{"comedy", "reaction", "reveal"}, []string{"oh_shit", "bass", "distorted", "vocal", "meme"}},
	{"1qGeGaV2boyAvX5xgRbJ2-ZSpmygWgbUZ", "sfx_gaming_hitmarker_01.mp3", "Hitmarker", "gaming", "hitmarker", "dramatic", "medium", []string{"dramatic_hit", "counter", "reaction"}, []string{"hitmarker", "gaming", "impact", "game", "meme"}},
	{"14mG59Ub6i8JYxxTvIEJHn5QzSsv8_AKE", "sfx_gaming_tf2_bonk_02.mp3", "TF2 Bonk", "gaming", "comic_impact", "comedic", "medium", []string{"comedy", "dramatic_hit", "reaction"}, []string{"tf2", "bonk", "gaming", "impact", "meme"}},
	{"1e7eZSFas-WokHJclqLxxeWcEV2AtBmTb", "sfx_cartoon_hiyakkk_scream_01.mp3", "Hiyakkk Scream", "cartoon", "vocal_scream", "shocked", "high", []string{"reaction", "reveal", "comedy"}, []string{"hiyakkk", "scream", "anime", "vocal", "energy"}},
	{"16Ykb6V8mX69vqhruEMw0oOKv-IZzG4Nd", "sfx_ambient_cave_echo_01.mp3", "Cave Ambience", "ambient", "cave_ambience", "spooky", "low", []string{"atmosphere", "suspense", "build_up"}, []string{"cave", "ambience", "echo", "minecraft", "spooky"}},
	{"1IGRXP_lc-MuoCbuh8Cqz4COC_Q4BgqcA", "sfx_foley_water_drip_01.mp3", "Water Droplet Drip", "foley", "water_drip", "neutral", "low", []string{"action_match", "texture", "atmosphere"}, []string{"water", "droplet", "drip", "cave", "foley"}},
	{"1QxXqQn6rXuldz2PbA5N5O7-cxNp70kDQ", "sfx_impact_katon_fire_blast_01.mp3", "Katon Fire Blast", "impact", "energy_blast", "dramatic", "high", []string{"dramatic_hit", "reveal", "predator"}, []string{"katon", "fire", "anime", "blast", "impact"}},
	{"1woSTtLQHMOB2hVP46LFbsYePcJoVAAwZ", "sfx_impact_nuke_bomb_01.mp3", "Nuke Bomb", "impact", "explosion_heavy", "dramatic", "high", []string{"dramatic_hit", "reveal", "predator"}, []string{"nuke", "bomb", "explosion", "blast", "gaming"}},
	{"13SHeZMfnDiIkR2OsVK_3XlYX8cjojQa1", "sfx_cartoon_brain_aneurysm_meme_01.mp3", "Brain Aneurysm Meme", "cartoon", "comic_reaction", "shocked", "medium", []string{"comedy", "reaction", "meme"}, []string{"brain", "aneurysm", "meme", "reaction"}},
	{"17iAzLglGSQLmKdQSpk_7YSJKzCmQGNZq", "sfx_cartoon_sonic_spring_01.mp3", "Sonic Spring", "cartoon", "boing_spring", "playful", "medium", []string{"comedy", "reaction", "motion"}, []string{"sonic", "spring", "boing", "cartoon", "bounce"}},
	{"1woFJJ7IpWCI9KQlYD0XP5LXkOVb-886E", "sfx_impact_exploding_toilet_01.mp3", "Exploding Toilet Meme", "impact", "comic_explosion", "comedic", "high", []string{"comedy", "dramatic_hit", "reaction"}, []string{"explosion", "toilet", "meme", "impact", "funny"}},
	{"1z3XpGrlajJALMk_gGlr3O7lzqZ3MgkdO", "sfx_cartoon_optimus_prides_meme_01.mp3", "Optimum Prides Meme", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"optimus", "prime", "prides", "vocal", "meme"}},
	{"1cEzfEHfJDNSe9XrynkXcDoClg3nTZB_A", "sfx_impact_basicbeam_fire_01.mp3", "Basicbeam Fire", "impact", "energy_blast", "dramatic", "high", []string{"dramatic_hit", "reveal", "motion"}, []string{"basicbeam", "fire", "blast", "energy", "impact"}},
	{"1NMM603MW9iMshdYtC6cXP4z3WJTwHgfy", "sfx_cartoon_ara_ara_01.mp3", "Ara Ara", "cartoon", "comic_reaction", "playful", "medium", []string{"comedy", "reaction", "meme"}, []string{"ara_ara", "anime", "female", "vocal", "japanese"}},
	{"1RNI62og6HwKcB9mDhJ571a9B-qkZO0q0", "sfx_foley_whip_crack_02.mp3", "Crack the Whip", "foley", "whip_crack", "action", "high", []string{"action_match", "motion", "dramatic_hit"}, []string{"whip", "crack", "snap", "foley", "sharp"}},
	{"16ndRa5N34KWg4vSkqNotDHLuhiPkaSZq", "sfx_cartoon_talking_ben_no_02.mp3", "Talking Ben No", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"talking_ben", "no", "vocal", "meme", "short"}},
	{"1AFckig6h0DeVkkIXltcfrMjEg_42QM2W", "sfx_foley_kiss_chuaks_01.mp3", "Chuaks Kiss", "foley", "kiss", "playful", "low", []string{"action_match", "comedy", "micro_accent"}, []string{"chuaks", "kiss", "vocal", "foley"}},
	{"14nxAmW9HGOfUBj7mkceCu_tLxsfhUeCz", "sfx_transition_sukuna_domain_expansion_01.mp3", "Sukuna Domain Expansion", "transition", "anime_reveal", "dramatic", "high", []string{"reveal", "rank_change", "dramatic_hit"}, []string{"sukuna", "jujutsu_kaisen", "anime", "domain_expansion", "vocal"}},
	{"16TDPcKc6um-sp2vfP5ItSNkZLRLVEwvb", "sfx_gaming_minecraft_click_01.mp3", "Minecraft Click", "gaming", "game_ui", "playful", "low", []string{"counter", "notification", "micro_accent"}, []string{"minecraft", "click", "gaming", "ui"}},
	{"1N0Bu9R8TiT-scpj1FnlCQ_tswPieGIPC", "sfx_glitch_distortion_max_01.mp3", "Maximum Distortion", "glitch", "digital_distortion", "technical", "high", []string{"glitch", "transition", "dramatic_hit"}, []string{"distortion", "static", "glitch", "noise", "crackle"}},
	{"1Jz5DIIoS82dDXR2OHKoP9En-68-d1Sxh", "sfx_ui_samsung_spaceline_notification_01.mp3", "Samsung Spaceline Notification", "ui", "notification_click", "clean", "low", []string{"notification", "label", "micro_accent"}, []string{"samsung", "spaceline", "notification", "ui", "chime"}},
	{"1fHBkvsVsQVeShBw6Eoi-oR17Mzzhg17H", "sfx_foley_sword_sound_01.mp3", "Sword Sound", "foley", "sword_clash", "action", "high", []string{"action_match", "dramatic_hit", "motion"}, []string{"sword", "slash", "metallic", "weapon", "clash"}},
	{"1YfbjqzR4a-81Ez-j7U93r5SvcmKsuprY", "sfx_ui_buy_confirm_01.mp3", "Buy Confirm", "ui", "purchase_confirm", "playful", "low", []string{"counter", "success", "notification"}, []string{"buy", "purchase", "confirm", "ui", "game"}},
	{"1mvyX15oObdK0NzJP4YKCIre-UPkozSR8", "sfx_cartoon_bo_womp_02.mp3", "Bo Womp", "cartoon", "fail_disappointment", "sad", "medium", []string{"comedy", "failure", "reaction"}, []string{"bo_womp", "fail", "sad", "cartoon", "comedy"}},
	{"1nsO8N0hXGgU-Qua8_EvW29D_vwraieSM", "sfx_cartoon_huh_confused_02.mp3", "Huh Confused", "cartoon", "comic_reaction", "confused", "medium", []string{"comedy", "reaction", "meme"}, []string{"huh", "confused", "vocal", "anime", "reaction"}},
	{"1AG6vh-3X1_GYuUGwUqM0Fz8JlHJkZbK2", "sfx_foley_sword_duel_challenge_01.mp3", "Sword Duel Challenge", "foley", "sword_clash", "action", "high", []string{"action_match", "dramatic_hit", "motion"}, []string{"sword", "duel", "challenge", "weapon", "metallic"}},
	{"1CP3zFA-43xYbyj9-rcXOfXHO9FkFRIB_", "sfx_misc_untitled_01.mp3", "Untitled Sound Effect", "misc", "meme_accent", "neutral", "medium", []string{"accent", "reaction", "transition"}, []string{"untitled", "sound_effect", "meme"}},
	{"1On7Nj_pcs7hsO852vRWTkKC1zfF84PGD", "sfx_ui_one_piece_ringtone_02.mp3", "One Piece Ringtone 2", "ui", "ringtone", "playful", "low", []string{"notification", "label", "micro_accent"}, []string{"one_piece", "ringtone", "notification", "ui"}},
	{"1PKB0whswZQCmveC3nfRmYfsKxAS6n3tY", "sfx_music_deja_vu_01.mp3", "Deja Vu Eurobeat", "music", "meme_theme", "energetic", "high", []string{"background", "montage", "motion"}, []string{"deja_vu", "eurobeat", "synth", "music", "meme", "fast"}},
	{"1MAla85bPW393UFnHWLdz48Upm1mTWTcd", "sfx_cartoon_ooo_maga_reaction_01.mp3", "Ooo Maga Reaction", "cartoon", "comic_reaction", "shocked", "medium", []string{"comedy", "reaction", "meme"}, []string{"ooo", "maga", "vocal", "reaction", "meme"}},
	{"1auibzbxdT6sr9Q4w2c8XHQarBNv1E3Dd", "sfx_cartoon_mongraal_shing_01.mp3", "Mongraal Shing", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"mongraal", "shing", "vocal", "meme"}},
	{"1eugr7lf-_ymTIHE2iYathsNhD5soCTBU", "sfx_impact_basicbeam_fire_02.mp3", "Basicbeam Fire 2", "impact", "energy_blast", "dramatic", "high", []string{"dramatic_hit", "reveal", "motion"}, []string{"basicbeam", "fire", "blast", "energy", "impact"}},
	{"1jys_wRwnpVb3bnf3Tbp7p6YhZL_iFn1Y", "sfx_foley_helicopter_meme_01.mp3", "Helicopter Meme", "foley", "rotor_engine", "action", "medium", []string{"action_match", "motion", "texture"}, []string{"helicopter", "rotor", "engine", "meme", "foley"}},
	{"1l4oPxdsL3mU_cA0wSNaZifhByaIXku9P", "sfx_ui_slot_machine_jackpot_01.mp3", "Slot Machine Jackpot", "ui", "reward_jackpot", "playful", "medium", []string{"reward", "counter", "success"}, []string{"slot_machine", "jackpot", "reward", "gaming", "ui"}},
	{"1zqPScvNatRYAIVXYZ9pBSbmWh4FavMvY", "sfx_ui_valorant_loading_01.mp3", "Valorant Loading Screen", "ui", "loading_loop", "technical", "low", []string{"notification", "transition", "micro_accent"}, []string{"valorant", "loading", "gaming", "ui"}},
	{"1AL3Tg4oxEGUCNpK0dyP9p4l6DUD9xMg4", "sfx_music_among_us_trap_01.mp3", "Among Us Trap Remix", "music", "meme_theme", "energetic", "high", []string{"background", "montage", "comedy"}, []string{"among_us", "trap", "bass", "music", "meme"}},
	{"1LgUzWscO0RFzKZoEhxmJ0QZeS29iDlJm", "sfx_cartoon_what_the_hell_02.mp3", "What the Hell Reaction 2", "cartoon", "comic_reaction", "shocked", "medium", []string{"comedy", "reaction", "meme"}, []string{"what_the_hell", "vocal", "meme", "reaction"}},
	{"1NQiyRZecs7Zb6owd96DLfeQmc23IBGVL", "sfx_cartoon_aw_hell_nah_01.mp3", "Aw Hell Nah", "cartoon", "comic_reaction", "shocked", "medium", []string{"comedy", "reaction", "meme"}, []string{"aw_hell_nah", "vocal", "meme", "reaction"}},
	{"1Qd8JxURWBQ-tTkRLjEwcdSV43o0kd6Gz", "sfx_music_gta_san_andreas_opening_01.mp3", "GTA San Andreas Opening", "music", "game_intro", "dramatic", "medium", []string{"background", "montage", "reveal"}, []string{"gta", "san_andreas", "opening", "game", "music"}},
	{"1MedRcJ3jChjL8w3j_tbGOQYvAd-ZrtTL", "sfx_foley_mail_bike_upin_ipin_01.mp3", "Mail Bike Upin Ipin", "foley", "vehicle_action", "playful", "medium", []string{"action_match", "motion", "comedy"}, []string{"mail", "bike", "upin_ipin", "vehicle", "foley"}},
	{"1VXk2vZOewaY0YSnlb3rG2pmqJHwa2SCL", "sfx_ui_poco_x3_phone_01.mp3", "Poco X3 Phone", "ui", "notification_click", "clean", "low", []string{"notification", "label", "micro_accent"}, []string{"poco", "phone", "notification", "ui"}},
	{"1X3TSD1ep4jrqF_MJS7im0sinNnQDivfM", "sfx_cartoon_gah_dayum_01.mp3", "Gah Dayum Reaction", "cartoon", "comic_reaction", "shocked", "medium", []string{"comedy", "reaction", "meme"}, []string{"gah_dayum", "vocal", "reaction", "meme"}},
	{"1YmAAAx0TPlDSyqmks3nRnLsWF3UPC-SG", "sfx_music_chinese_rap_01.mp3", "Chinese Rap Song", "music", "meme_theme", "energetic", "medium", []string{"background", "montage", "comedy"}, []string{"chinese", "rap", "song", "music", "meme"}},
	{"1_qG7VAprjweJ805VQKsC8lzzIQDfhrCA", "sfx_music_epic_sax_gandalf_01.mp3", "Epic Sax Gandalf", "music", "meme_theme", "energetic", "high", []string{"background", "montage", "motion"}, []string{"epic_sax", "gandalf", "sax", "music", "meme"}},
	{"1qWm2juVKRTa03S8NJb0Ac2ZTNIGYhVAb", "sfx_cartoon_one_piece_angry_01.mp3", "One Piece Angry", "cartoon", "comic_reaction", "angry", "high", []string{"comedy", "reaction", "reveal"}, []string{"one_piece", "angry", "vocal", "anime", "reaction"}},
	{"1Kz2AoPhBt_cl5Rcs5XZBAdwtAbkKveKa", "sfx_cartoon_kobo_chamber_meme_01.mp3", "Kobo Chamber Meme", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"kobo", "chamber", "vocal", "meme"}},
	{"1OaBZk63bIYExEf9j18LD-4N9fawpcwo4", "sfx_cartoon_sheesh_reaction_01.mp3", "Sheesh Reaction", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"sheesh", "vocal", "reaction", "meme"}},
	{"1N9_V6_LWqPe6rHLQisA1-FPYyNZ0Lswv", "sfx_foley_kids_cheering_02.mp3", "Kids Cheering", "foley", "crowd_cheer", "celebratory", "medium", []string{"success", "celebration", "reaction"}, []string{"kids", "cheering", "applause", "crowd", "celebration"}},
	{"1TFA-iPw7wvkUlz_0-ER-R5wDWiztAF6k", "sfx_cartoon_wait_what_hell_01.mp3", "Wait What the Hell", "cartoon", "comic_reaction", "shocked", "medium", []string{"comedy", "reaction", "meme"}, []string{"wait", "what", "hell", "vocal", "meme"}},
	{"17uUhIg4SwoqnPMch1vVyGvUSSDn0DpNK", "sfx_music_unknown_music_01.mp3", "Unknown Music 2", "music", "meme_theme", "neutral", "medium", []string{"background", "montage", "mood"}, []string{"music", "unknown", "meme"}},
	{"1YWlitp6hnR53uvNchnPWqbXAnl6f6FYY", "sfx_misc_tmpq7mpzzl_01.mp3", "Tmpq7mpzzl", "misc", "meme_accent", "neutral", "medium", []string{"accent", "reaction", "transition"}, []string{"tmpq7mpzzl", "sound_effect", "meme"}},
	{"1lukwc2Lz_dDuo8FZuzl30HdZ6iLbQ886", "sfx_misc_tmpq7mpzzl_02.mp3", "Tmpq7mpzzl 2", "misc", "meme_accent", "neutral", "medium", []string{"accent", "reaction", "transition"}, []string{"tmpq7mpzzl", "sound_effect", "meme"}},
	{"1u0kp9IgbnhYhKJb3sO5VIT5L9SFwGdEd", "sfx_music_one_piece_cornered_raid_01.mp3", "One Piece Cornered Raid OST", "music", "anime_theme", "dramatic", "medium", []string{"background", "montage", "reveal"}, []string{"one_piece", "ost", "cornered", "raid", "anime", "music"}},
	{"1vXCIIBOomRQsqqQyBgKB94vGwD4Sbqah", "sfx_ambient_dream_sound_01.mp3", "Dream Sound", "ambient", "dream_ambience", "calm", "low", []string{"atmosphere", "mood", "build_up"}, []string{"dream", "ambient", "sound", "atmosphere"}},
	{"1fgOI6gc-uSaLLxwYP4FpMu_9jw5Alnp6", "sfx_music_spiderman_meme_song_01.mp3", "Spider-Man Meme Song", "music", "meme_theme", "energetic", "medium", []string{"background", "montage", "comedy"}, []string{"spiderman", "song", "meme", "music"}},
	{"16q8s7l9pqiDm2d-ReVCZNPqGIdaSRLfF", "sfx_cartoon_illuminati_confirmed_mlg_01.mp3", "Illuminati Confirmed MLG", "cartoon", "comic_reaction", "comedic", "high", []string{"comedy", "reaction", "meme"}, []string{"illuminati", "confirmed", "mlg", "gaming", "meme"}},
	{"17H9Q2MIjUp6kkQzu71lrvuqyp-hLZEvt", "sfx_ui_transponder_snail_01.mp3", "Transponder Snail", "ui", "notification_click", "playful", "low", []string{"notification", "label", "micro_accent"}, []string{"transponder", "snail", "one_piece", "notification", "ui"}},
	{"1Afvei36u4dbxnj8zJtptLY83B2krlIge", "sfx_music_sigma_01.mp3", "Sigma Meme Music", "music", "meme_theme", "energetic", "medium", []string{"background", "montage", "comedy"}, []string{"sigma", "music", "meme", "edit"}},
	{"1qICjalis8N6hvHzHi7y6_pe9F1uDv0aN", "sfx_impact_outro_pumpme_kick_01.mp3", "Outro Pumpme Kick", "impact", "cinematic_boom", "dramatic", "high", []string{"dramatic_hit", "reveal", "rank_change"}, []string{"outro", "kick", "impact", "bass", "transition"}},
	{"1ClrVJCTb_apJe1_qQ5mWQuSbAZhmzHzm", "sfx_cartoon_he_walk_meme_01.mp3", "He Walk Meme", "cartoon", "comic_reaction", "comedic", "medium", []string{"comedy", "reaction", "meme"}, []string{"he_walk", "meme", "vocal", "comedy"}},
	{"1QMeemi5WyFVTrxLmgSAYFY--mSQA1rYM", "sfx_music_all_my_fellaz_02.mp3", "All My Fellaz", "music", "meme_theme", "playful", "medium", []string{"background", "montage", "comedy"}, []string{"all_my_fellaz", "vocal", "music", "meme"}},
	{"1RXDgVSq4wvM8qC6dk_cFjAYqzaT6nLQE", "sfx_music_bernyanyi_bernyayi_01.mp3", "Bernyanyi Bernyayi", "music", "meme_theme", "playful", "medium", []string{"background", "montage", "comedy"}, []string{"bernyanyi", "bernyayi", "singing", "music", "meme"}},
	{"1UDKT3YjwjUYMluFMSHxAO1py4cLYnI1U", "sfx_music_spongebob_sad_song_01.mp3", "SpongeBob Sad Song", "music", "sad_theme", "sad", "low", []string{"background", "mood", "failure"}, []string{"spongebob", "sad", "song", "music", "cartoon"}},
	{"1oCpcAA8Qha_Mgx4pz27LZyAGq2agJBPo", "sfx_music_naruto_sad_music_01.mp3", "Naruto Sad Music", "music", "anime_theme", "sad", "low", []string{"background", "mood", "failure"}, []string{"naruto", "sad", "anime", "music", "theme"}},
}

func runIndexProvidedSoundEffects(args []string) error {
	_ = args
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.Drive == nil || root.Drive.Reader == nil || root.Drive.Admin == nil || root.Repos == nil || root.Repos.ClipsRepo == nil || root.Outbox == nil || root.Outbox.Dispatcher == nil || root.Outbox.EventsPool == nil || root.Outbox.EventsRepo == nil {
		return fmt.Errorf("Drive, clips repository and outbox are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	deadLettersBefore, err := root.Outbox.EventsRepo.CountByEventTypeAndStatus(ctx, "asset.index.requested", "dead_letter")
	if err != nil {
		return fmt.Errorf("read outbox baseline: %w", err)
	}
	go root.Outbox.EventsPool.Start(ctx, 1)
	defer func() { _ = root.Outbox.EventsPool.Stop(15 * time.Second) }()

	localRoot := cfg.Storage.FullPath(filepath.Join(cfg.Storage.MediaDir, "sound_effects"))
	if err := os.MkdirAll(localRoot, 0o755); err != nil {
		return fmt.Errorf("create sound effects directory: %w", err)
	}
	prober := rustexec.NewVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log)
	for _, spec := range providedSoundEffects {
		meta, err := root.Drive.Reader.GetFileMeta(ctx, spec.driveID)
		if err != nil {
			return fmt.Errorf("read Drive metadata %s: %w", spec.driveID, err)
		}
		localPath := filepath.Join(localRoot, spec.filename)
		body, _, err := root.Drive.Reader.DownloadFile(ctx, spec.driveID)
		if err != nil {
			return fmt.Errorf("download %s: %w", spec.name, err)
		}
		out, err := os.Create(localPath)
		if err == nil {
			_, err = io.Copy(out, body)
		}
		closeBodyErr := body.Close()
		if err == nil {
			err = closeBodyErr
		}
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return fmt.Errorf("save %s: %w", spec.name, err)
		}
		folderID, err := root.Drive.Admin.GetOrCreateFolder(ctx, displaySoundEffectFamily(spec.family), soundEffectsDriveFolderID)
		if err != nil {
			return fmt.Errorf("create Drive family folder %s: %w", spec.family, err)
		}
		duration, err := probeSoundEffectDuration(ctx, prober, localPath)
		if err != nil {
			return fmt.Errorf("probe %s: %w", spec.name, err)
		}
		hash, err := sha256File(localPath)
		if err != nil {
			return fmt.Errorf("hash %s: %w", spec.name, err)
		}
		now := time.Now().UTC()
		clip := &asset.Asset{ID: spec.driveID, Name: spec.name, Filename: spec.filename, Source: asset.Source("sound_effect"), MediaType: asset.MediaType("sound_effect"), Category: "file", Group: spec.family, Duration: duration, LifecycleState: asset.StateActive, CreatedAt: now, UpdatedAt: now, Tags: spec.tags}
		clip.SearchText = strings.Join(append([]string{spec.name, spec.family, spec.subtype, spec.mood, spec.energy}, append(spec.bestFor, spec.tags...)...), " ")
		clip.SetDriveFileID(spec.driveID)
		clip.SetDriveLink("https://drive.google.com/file/d/" + spec.driveID + "/view")
		clip.SetDownloadLink("https://drive.google.com/uc?export=download&id=" + spec.driveID)
		clip.SetLocalPath(localPath)
		clip.SetFileHash(hash)
		clip.SetParentFolderID(folderID)
		clip.SetMetadataString("mime_type", meta.MimeType)
		clip.SetMetadataString("source_name", meta.Name)
		clip.SetMetadataString("sfx_family", spec.family)
		clip.SetMetadataString("sfx_category", spec.family)
		clip.SetMetadataString("sfx_subtype", spec.subtype)
		clip.SetMetadataString("sfx_mood", spec.mood)
		clip.SetMetadataString("sfx_energy", spec.energy)
		clip.SetMetadataString("sfx_best_for", strings.Join(spec.bestFor, ","))
		clip.SetMetadataString("sfx_tags", strings.Join(spec.tags, ","))
		if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, hash); err != nil {
			return fmt.Errorf("index %s: %w", spec.name, err)
		}
		if len(meta.Parents) > 0 && meta.Parents[0] != folderID {
			if err := root.Drive.Admin.MoveFile(ctx, spec.driveID, meta.Parents[0], folderID); err != nil {
				return fmt.Errorf("move %s to %s: %w", spec.name, spec.family, err)
			}
		}
		fmt.Printf("indexed %s (Drive: %s) family=%s subtype=%s duration=%.3fs\n", spec.name, meta.Name, spec.family, spec.subtype, duration.Seconds())
	}
	if err := waitForAssetIndexOutbox(ctx, root, deadLettersBefore); err != nil {
		return err
	}
	return nil
}
