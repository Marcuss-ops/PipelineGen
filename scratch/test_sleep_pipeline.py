import urllib.request
import json

url = "http://127.0.0.1:8081/api/script/generate-from-source"
source_text = """Sleep without pills starts long before you turn out the light. It is built in daylight, in the small choices about caffeine and screens, in the way you teach your brain that bed means sleep and nothing else.

Pills can knock you out, but they rarely teach your nervous system how to fall asleep on its own. The work is less dramatic and more reliable: anchor your body clock, cool the room, empty the mind just enough, and repeat.

## 1. Anchor your clock with light

Your circadian rhythm wants a clear morning signal and a clear evening signal.

**Morning:** get outside within 30 to 60 minutes of waking, even on cloudy days in Campania. Ten minutes of real daylight tells the suprachiasmatic nucleus to drop melatonin and raise cortisol at the right time. No sunglasses, no window glass if you can help it. If you wake before sunrise, turn on bright, warm-white lights and move.

**Evening:** do the opposite. Two to three hours before bed, dim overheads to lamp level. Phones and laptops are the main problem because of blue light and because they keep your brain solving. Use night mode, but better, put them in another room at a set time. Your brain reads darkness as permission to make melatonin.

Keep wake time boringly consistent, even on weekends. Bedtime can flex, wake time should not. A stable wake time does more for insomnia than any supplement.

## 2. Build a wind-down you will actually keep

Sleep is not an on-off switch. You need a ramp.

Pick a 60 to 90 minute sequence and do it in the same order. The predictability is the point.

- Light stretch or slow breathing for five minutes
- Warm shower, the drop in skin temperature afterward mimics the natural evening dip
- Change into sleep clothes, brush teeth, same playlist or podcast at low volume
- Write tomorrow's to-dos on paper, not your phone, so your brain stops rehearsing them

If you watch something, make it familiar and low stakes. New plots keep you alert. The goal is not relaxation as a feeling, it is repetition as a cue.

## 3. Make the room a cave

Temperature matters more than mattress price. Core body temperature needs to fall about 1°C to initiate sleep. Aim for 17 to 19°C, use breathable cotton or linen, socks if your feet run cold.

Darkness: blackout curtains or a soft eye mask. Even small LEDs register. Cover them.

Sound: steady is better than silent. A fan, brown noise, or earplugs beats a street that spikes at 2 a.m. In Castellammare, summer scooters will test you. Plan for it.

Reserve the bed for two things only: sleep and intimacy. No work emails, no scrolling, no arguing. Your brain learns associations fast. If bed equals alert, you will stay alert.

## 4. Caffeine, alcohol, and food timing

Caffeine has a half-life of about five to seven hours. That 4 p.m. espresso is still a quarter strength at midnight. Set a hard stop eight hours before bed. Switch to decaf or, better, water and a walk.

Alcohol helps you fall asleep but fragments the second half of the night. It suppresses REM, raises heart rate, and you wake at 3 a.m. thirsty. If you drink, finish two to three hours before bed and keep it to one.

Big late dinners raise core temperature and reflux. Finish your main meal three hours before sleep. A small carb-plus-protein snack like yogurt or a banana is fine if you are genuinely hungry.

## 5. Move in the day, calm at night

Physical fatigue is honest. Aim for 30 minutes of movement that raises your heart rate, ideally in daylight. Morning or early afternoon workouts advance your clock in a helpful way.

Avoid hard training within three hours of bedtime. The adrenaline and heat linger. If evenings are your only window, keep it to zone 2, then follow with a cool shower and longer wind-down.

Even on rest days, get steps. The body that sits all day often lies awake all night.

## 6. Unstick the mental loop

Most sleeplessness is not a lack of tiredness, it is a loop of monitoring. You check, am I asleep yet, and that checking wakes you.

Three tools work without pills:

**Worry download:** at 8 p.m., set a timer for ten minutes. Write every open loop, then write the next tiny action for each. Close the notebook. When worries pop up later, remind yourself they have a place tomorrow.

**Cognitive shuffle:** instead of counting sheep, picture random neutral objects, apple, mountain, spoon, for five seconds each. The brain cannot ruminate and generate images at once. It is shadow-free.

**NSDR or slow breathing:** lie down, inhale 4 seconds, exhale 6 seconds, for five minutes. Longer exhales activate the parasympathetic system. Non-sleep deep rest tracks on YouTube or apps are fine, just keep the screen dark.

If anxiety about sleep persists for weeks, or you notice snoring, gasping, leg jerks, or severe daytime sleepiness, talk with a clinician. Sleep apnea and other conditions need medical assessment, not better habits alone.

## 7. Naps without wrecking the night

Naps are useful if you keep them short and early. Twenty minutes, before 3 p.m., set an alarm. Longer or later naps steal pressure from the night. If you are in a debt cycle, skip naps for a week to rebuild drive, then reintroduce them carefully.

## 8. What to do at 3 a.m.

Do not stay in bed awake. After about 20 minutes, get up, keep lights dim, go to another room, and do something dull: read a paper book you have read before, fold laundry, listen to a calm podcast at low volume. No phone, no clock watching. Return to bed only when sleepy, not just tired. This retrains the bed-sleep link, the core of CBT-I, the gold standard for insomnia.

If you wake to use the bathroom, use a nightlight, not the bright overhead. Avoid checking the time. The number adds pressure."""

data = {
    "source_text": source_text.strip(),
    "language": "en",
    "style": "whiteboard",
    "visual_style": "whiteboard",
    "scene_count": 100,
    "width": 1024,
    "height": 1024,
    "title": "Sleep Without Pills",
    "output_name": "sleep-without-pills"
}

req = urllib.request.Request(
    url, 
    data=json.dumps(data).encode("utf-8"),
    headers={"Content-Type": "application/json"}
)

print("Triggering Go pipeline endpoint with Sleep Routine in Whiteboard Style...")
try:
    with urllib.request.urlopen(req, timeout=1200) as resp:
        res = json.loads(resp.read().decode())
        print("Pipeline Execution Success!")
        print("Document URL:", res.get("doc_url"))
        print("Markdown Path:", res.get("markdown_path"))
        print("JSON Path:", res.get("json_path"))
        print("Generated scenes count:", len(res.get("scenes", [])))
except Exception as e:
    print("Error:", e)
    if hasattr(e, "read"):
        print("Detail:", e.read().decode())
