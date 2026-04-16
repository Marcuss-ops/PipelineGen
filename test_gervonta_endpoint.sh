#!/bin/bash
# Test endpoint API con testo Gervonta Davis completo
# Verifica: segmentazione, entità, clip association, Stock folders

set -e

cd /home/pierone/Pyt/VeloxEditing/refactored/src/go-master

echo "========================================="
echo "🧪 Test Endpoint: Gervonta Davis Pipeline"
echo "========================================="
echo ""

# Testo completo Gervonta Davis
SCRIPT_TEXT='From Nothing

He was born Gervonta Bryant Davis on November 7, 1994, not into boxing royalty but into Sandtown-Winchester, West Baltimore, one of the most violent zip codes in America. The official biography puts it plainly: Davis was raised in Sandtown-Winchester, his parents were drug addicts and were frequently in and out of jail. He has spoken about bouncing between homes, and reporting from his hometown notes he grew up in a foster home due to his father absence and faced early struggles with substance abuse.

Boxing was not a hobby. It was daycare, then discipline, then salvation. At five years old he walked into Upton Boxing Center, a converted gym on Pennsylvania Avenue, and met Calvin Ford, the man who would become trainer, father figure, and legal guardian in practice if not on paper. Ford is famous enough to have inspired Dennis Cutty Wise on The Wire, but in real life his work was quieter: keeping kids off corners.

Davis stayed. While other kids quit, he compiled an amateur record that looks fake on paper: 206 wins, 15 losses. He won the 2012 National Golden Gloves Championship, three straight National Silver Gloves from 2006 to 2008, two National Junior Olympics gold medals, two Police Athletic League Championships, and two Ringside World Championships. He attended Digital Harbor High School, a magnet school, but dropped out to focus on fighting, later earning a GED.

That background explains everything about his style. He never learned boxing as a sport first. He learned it as survival. Southpaw, compact at five foot five, with a 67-inch reach, he fought like someone who expected to be crowded, disrespected, and needed to end things early.

To Everything

He turned pro at 18, on February 22, 2013, against Desi Williams at the D.C. Armory, and won via first-round knockout. By August 2014 he was 8-0, all inside the distance. Floyd Mayweather Jr. saw the tape and signed him to Mayweather Promotions in 2015, putting him on the undercard of Mayweather-Berto that September where Davis needed 94 seconds to stop Recky Dulay.

The rise was violent and fast. On January 14, 2017, at Barclays Center, the 22-year-old challenged undefeated IBF super featherweight champion Jose Pedraza. Davis defeated Pedraza in a seventh-round KO to win the IBF super featherweight title. Mayweather, at ringside, called him the future of boxing.

What followed was a rare kind of dominance across weight. The record books now list him as a holder of the IBF super featherweight title in 2017, the WBA super featherweight title twice between 2018 and 2020, the WBA super lightweight title in 2021, and the WBA lightweight title from 2023 to 2026.

He was not always professional. He missed weight for Liam Walsh in London in 2017, then missed by two pounds for Francisco Fonseca on the Mayweather-McGregor card and was stripped on the scale. He still knocked Fonseca out in eight. He was chaos and control in the same night.

But when he was on, he was must-see. Three moments built the empire:

Leo Santa Cruz, October 31, 2020. Alamodome, pandemic era. Davis retained his WBA lightweight title and won the WBA super featherweight title with a left uppercut in round six that is still replayed as a perfect punch. The PPV did 225,000 buys.

Mario Barrios, June 26, 2021. Moving up to 140 pounds, Davis stopped the bigger Barrios in the 11th to win the WBA super lightweight title. He became a three-division champion at 26.

Ryan Garcia, April 22, 2023. This was the cultural peak. T-Mobile Arena, Showtime and DAZN joint PPV, two undefeated social-media stars in their prime. Davis won by KO in round 7. The fight did 1,200,000 buys and 87 million dollars in revenue, the biggest boxing event of the year.

By then Tank was no longer just a fighter. He was a Baltimore homecoming, he headlined Royal Farms Arena in 2019, the first world title fight in the city in 80 years, he was Under Armour deals, 3.4 million Instagram followers, a 3.4 million dollar Baltimore condo, and a knockout rate of 93 percent. He split from Mayweather in 2022, bet on himself, and kept winning: Rolando Romero in six, Hector Garcia by RTD in January 2023, Frank Martin by KO in eight on June 15, 2024.

He also changed personally. On December 24, 2023, Davis converted to Islam and adopted the Muslim name Abdul Wahid. He spoke more about fatherhood, he has three children, a daughter with Andretta Smothers and a daughter and son with Vanessa Posso.

For a kid from Sandtown who had once lived in foster care, this was everything. Money, belts in three divisions, the Mayweather co-sign then independence, and the rare ability to make casual fans tune in just to see if someone would get flattened.

The first crack in the ring came not from a loss but from a draw. On March 1, 2025, at Barclays Center, Lamont Roach Jr. took him 12 rounds and the judges called it a majority draw. Davis retained the WBA lightweight title, but for the first time in 31 fights he did not have his hand raised. He had not fought since.

Losing Everything

The outside-the-ring story had been building for almost a decade, parallel to the knockouts.'

echo "📤 Testing /api/script/generate-from-clips..."
echo ""

RESPONSE=$(curl -s -w "\nHTTP_CODE:%{http_code}" -X POST http://localhost:8080/api/script/generate-from-clips \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "Gervonta Davis",
    "language": "English",
    "tone": "documentary",
    "target_duration": 80,
    "clips_per_segment": 3,
    "use_artlist": true,
    "use_drive_clips": true
  }')

HTTP_CODE=$(echo "$RESPONSE" | grep "HTTP_CODE:" | cut -d: -f2)
BODY=$(echo "$RESPONSE" | sed '/HTTP_CODE:/d')

echo "📥 HTTP Status: $HTTP_CODE"
echo ""

# Pretty print JSON if jq available
if command -v jq &> /dev/null; then
    echo "$BODY" | jq '.'
else
    echo "$BODY"
fi

echo ""
echo "========================================="
echo "✅ Test completed"
echo "========================================="
